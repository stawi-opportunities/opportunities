// apps/api/cmd/flags_admin.go
//
// User-facing scam-flagging + operator triage. Wired alongside the
// source-admin surface (registerSourcesAdmin) when the api opens its
// own database connection. Public POST /opportunities/{slug}/flag
// requires JWT-attached profile_id; admin /admin/flags/* require the
// same Bearer-token check as the source-admin endpoints.
//
// Threshold rule: when an insert pushes the unresolved scam-flag count
// for a slug to >= domain.FlagAutoActionThreshold, the api emits an
// OpportunityAutoFlaggedV1 event (best-effort) and structured-logs
// the auto-action so operators see it in OpenObserve immediately.
// Materializer subscribes to the event and drops the row from search.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// flagAdminRepo is the narrow slice of repository.FlagRepository the
// handlers use. Pulled into an interface so tests can swap an in-memory
// fake without standing up Postgres.
type flagAdminRepo interface {
	Create(ctx context.Context, f *domain.OpportunityFlag) error
	GetByID(ctx context.Context, id string) (*domain.OpportunityFlag, error)
	ExistsForUser(ctx context.Context, slug, submittedBy string) (bool, error)
	ListByOpportunity(ctx context.Context, slug string) ([]*domain.OpportunityFlag, error)
	ListUnresolved(ctx context.Context, reason domain.FlagReason, slug string, limit, offset int) ([]*domain.OpportunityFlag, error)
	CountUnresolvedByOpportunity(ctx context.Context, slug string) (int, error)
	Resolve(ctx context.Context, id, by string, action domain.FlagResolutionAction) error
	ResolveAllForOpportunity(ctx context.Context, slug, by string, action domain.FlagResolutionAction) (int64, error)
	TopFlagged(ctx context.Context, limit int) ([]repository.TopFlaggedRow, error)
}

// jobsManticoreLookup is the narrow slice of jobsManticore the flag
// admin uses to resolve a slug → opportunity row (kind discriminator)
// when annotating a freshly-submitted flag. GetByID accepts either a
// canonical_id or a slug — the flag handler always passes a slug.
type jobsManticoreLookup interface {
	GetByID(ctx context.Context, id string) (*job, error)
}

// flagEventEmitter is the auto-flag event emit interface. The
// production wiring uses Frame's EventsManager when available; tests
// pass a recorder. nil is safe — the threshold logic skips emit and
// just structured-logs.
type flagEventEmitter interface {
	Emit(ctx context.Context, topic string, payload any) error
}

// frameEmitter adapts *frame.Service to flagEventEmitter.
type frameEmitter struct{ svc *frame.Service }

func (f frameEmitter) Emit(ctx context.Context, topic string, payload any) error {
	if f.svc == nil || f.svc.EventsManager() == nil {
		return errors.New("flagEmitter: frame events manager unavailable")
	}
	return f.svc.EventsManager().Emit(ctx, topic, payload)
}

// flagsAdmin bundles dependencies for both the public flag endpoint
// and the admin triage endpoints.
type flagsAdmin struct {
	repo        flagAdminRepo
	sourceRepo  sourceAdminRepo // reused for ban_source action
	jobs        jobsManticoreLookup
	emitter     flagEventEmitter
}

// registerFlagsAdmin wires the user-facing /opportunities/{slug}/flag
// endpoint and every /admin/flags/* + /admin/opportunities/* admin
// route. Mirrors registerSourcesAdmin's "open DB pool, build repo,
// register handlers" structure so both surfaces share the same Frame
// configuration and auth pattern.
func registerFlagsAdmin(ctx context.Context, mux *http.ServeMux, jm *jobsManticore) {
	log := util.Log(ctx)

	fc, err := fconfig.FromEnv[fconfig.ConfigurationDefault]()
	if err != nil {
		log.WithError(err).Warn("flags admin: frame config parse failed; flag endpoints disabled")
		return
	}

	// Build a Frame service so we get both the datastore manager AND
	// (best-effort) the events manager. EventsManager will be nil
	// unless Frame was started with a publisher registered — that's
	// OK, the threshold path falls back to log-only auto-action.
	_, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&fc), frame.WithDatastore())

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Warn("flags admin: no datastore pool; flag endpoints disabled")
		return
	}
	if err := pool.DB(ctx, false).AutoMigrate(&domain.OpportunityFlag{}); err != nil {
		log.WithError(err).Warn("flags admin: auto-migrate opportunity_flags failed; flag endpoints disabled")
		return
	}

	repo := repository.NewFlagRepository(pool.DB)
	srcRepo := repository.NewSourceRepository(pool.DB)

	a := &flagsAdmin{
		repo:       repo,
		sourceRepo: srcRepo,
		jobs:       jm,
		emitter:    frameEmitter{svc: svc},
	}

	// Public endpoint — auth required (JWT in Authorization header).
	mux.HandleFunc("POST /opportunities/{slug}/flag", a.handleUserFlag)

	// Admin endpoints — Bearer token enforced via requireAdmin.
	mux.HandleFunc("GET /admin/flags", requireAdmin(a.handleListUnresolved))
	mux.HandleFunc("GET /admin/flags/{id}", requireAdmin(a.handleGetFlag))
	mux.HandleFunc("POST /admin/flags/{id}/resolve", requireAdmin(a.handleResolve))
	mux.HandleFunc("GET /admin/opportunities/{slug}/flags", requireAdmin(a.handleListForOpportunity))
	mux.HandleFunc("GET /admin/opportunities/top-flagged", requireAdmin(a.handleTopFlagged))

	log.Info("flags admin: endpoints registered (/opportunities/{slug}/flag + /admin/flags/*)")
}

// userFlagRequest is the body shape for POST /opportunities/{slug}/flag.
type userFlagRequest struct {
	Reason      domain.FlagReason `json:"reason"`
	Description string            `json:"description"`
}

// handleUserFlag is the public-facing flag endpoint. Auth is REQUIRED
// (anonymous flagging is disabled to prevent abuse). On a duplicate
// flag from the same user, returns 409. On the third+ unresolved scam
// flag for the slug, emits OpportunityAutoFlaggedV1 (best-effort).
func (a *flagsAdmin) handleUserFlag(w http.ResponseWriter, r *http.Request) {
	profileID := profileIDFromJWT(r)
	if profileID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized",
			"flagging requires an authenticated profile_id")
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}

	var req userFlagRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if !domain.IsKnownFlagReason(req.Reason) {
		writeError(w, http.StatusBadRequest, "invalid_reason",
			fmt.Sprintf("reason must be one of: scam, expired, duplicate, spam, other (got %q)", req.Reason))
		return
	}
	if len(req.Description) > domain.FlagDescriptionMaxLen {
		writeError(w, http.StatusBadRequest, "description_too_long",
			fmt.Sprintf("description must be ≤ %d chars (got %d)",
				domain.FlagDescriptionMaxLen, len(req.Description)))
		return
	}

	// Fast-path 409 — checked BEFORE looking up the canonical kind so
	// duplicates don't pay for the search-index round-trip.
	exists, err := a.repo.ExistsForUser(r.Context(), slug, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "already_flagged",
			"this profile has already flagged this opportunity")
		return
	}

	// Resolve the kind + canonical_id from the canonical row
	// (best-effort — missing values are non-fatal, the flag still
	// records). The canonical_id lets the auto-flag emit carry the
	// pk-mappable id so the materializer can update Manticore by
	// hashID rather than by slug-string.
	kind, canonicalID := "", ""
	if a.jobs != nil {
		if j, _ := a.jobs.GetByID(r.Context(), slug); j != nil {
			kind = j.Kind
			canonicalID = j.CanonicalID
		}
	}

	flag := &domain.OpportunityFlag{
		OpportunitySlug: slug,
		OpportunityKind: kind,
		SubmittedBy:     profileID,
		Reason:          req.Reason,
		Description:     req.Description,
	}
	if err := a.repo.Create(r.Context(), flag); err != nil {
		// Race condition guard: between ExistsForUser and Create another
		// concurrent request may have inserted. Treat the unique-index
		// violation as a 409 too.
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "already_flagged",
				"this profile has already flagged this opportunity (race)")
			return
		}
		writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}

	util.Log(r.Context()).
		WithField("slug", slug).
		WithField("kind", kind).
		WithField("reason", string(req.Reason)).
		WithField("submitted_by", profileID).
		WithField("flag_id", flag.ID).
		Info("opportunity flag submitted")

	// Threshold check + auto-action emit. Done synchronously so the
	// response can carry the auto-action signal; the emit itself uses a
	// short-bounded context so a slow event bus can't add user-facing
	// latency.
	autoActioned := false
	if req.Reason == domain.FlagScam {
		count, err := a.repo.CountUnresolvedByOpportunity(r.Context(), slug)
		if err != nil {
			util.Log(r.Context()).WithError(err).
				WithField("slug", slug).
				Warn("flag threshold check failed")
		} else if count >= domain.FlagAutoActionThreshold {
			autoActioned = a.emitAutoFlag(r.Context(), canonicalID, slug, kind, count)
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":            flag.ID,
		"slug":          slug,
		"kind":          kind,
		"reason":        flag.Reason,
		"auto_actioned": autoActioned,
	})
}

// emitAutoFlag publishes OpportunityAutoFlaggedV1 (best-effort). Returns
// true if the emit succeeded — false on missing emitter or transport
// error. The structured-log line is unconditional so operators always
// see the threshold trip in OpenObserve regardless of the event-bus
// state.
func (a *flagsAdmin) emitAutoFlag(ctx context.Context, opportunityID, slug, kind string, count int) bool {
	log := util.Log(ctx).
		WithField("opportunity_id", opportunityID).
		WithField("slug", slug).
		WithField("kind", kind).
		WithField("flag_count", count)
	log.Warn("opportunity auto-flagged: threshold tripped")

	if a.emitter == nil {
		return false
	}
	emitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	env := eventsv1.NewEnvelope(eventsv1.TopicOpportunityAutoFlagged,
		eventsv1.OpportunityAutoFlaggedV1{
			OpportunityID: opportunityID,
			Slug:          slug,
			Kind:          kind,
			FlagCount:     count,
			FlaggedAt:     time.Now().UTC(),
		})
	if err := a.emitter.Emit(emitCtx, eventsv1.TopicOpportunityAutoFlagged, env); err != nil {
		log.WithError(err).Warn("opportunity auto-flag emit failed")
		return false
	}
	return true
}

// handleListUnresolved is GET /admin/flags. Filters: reason, slug,
// limit, offset.
func (a *flagsAdmin) handleListUnresolved(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	reason := domain.FlagReason(q.Get("reason"))
	slug := q.Get("slug")
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	if reason != "" && !domain.IsKnownFlagReason(reason) {
		writeError(w, http.StatusBadRequest, "invalid_reason",
			fmt.Sprintf("unknown reason %q", reason))
		return
	}

	flags, err := a.repo.ListUnresolved(r.Context(), reason, slug, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"flags":  flags,
		"count":  len(flags),
		"limit":  limit,
		"offset": offset,
	})
}

// handleGetFlag is GET /admin/flags/{id}.
func (a *flagsAdmin) handleGetFlag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flag, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if flag == nil {
		writeError(w, http.StatusNotFound, "not_found", "flag not found")
		return
	}
	writeJSON(w, http.StatusOK, flag)
}

// handleListForOpportunity is GET /admin/opportunities/{slug}/flags —
// every flag (resolved or not) for one slug, for context when an
// operator is reviewing.
func (a *flagsAdmin) handleListForOpportunity(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	flags, err := a.repo.ListByOpportunity(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":  slug,
		"flags": flags,
		"count": len(flags),
	})
}

// resolveRequest is the body shape for POST /admin/flags/{id}/resolve.
//
// SourceID is optional and only consulted when Action == "ban_source"
// — the canonical row in Manticore doesn't carry source_id today, so
// operators must point us at the offending source explicitly. A
// follow-up that stores source_id on the canonical doc can drop this
// requirement.
type resolveRequest struct {
	Action   domain.FlagResolutionAction `json:"action"`
	Note     string                      `json:"note"`
	SourceID string                      `json:"source_id,omitempty"`
}

// handleResolve is POST /admin/flags/{id}/resolve. Action is one of
// "ignore", "hide", "ban_source". The ban_source action additionally
// stops the source the bad opportunity came from and bulk-resolves
// every flag pointing at any opportunity from that source.
func (a *flagsAdmin) handleResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req resolveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if !domain.IsKnownFlagAction(req.Action) {
		writeError(w, http.StatusBadRequest, "invalid_action",
			fmt.Sprintf("action must be one of: ignore, hide, ban_source (got %q)", req.Action))
		return
	}

	flag, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if flag == nil {
		writeError(w, http.StatusNotFound, "not_found", "flag not found")
		return
	}

	operator := profileIDFromJWT(r)
	if operator == "" {
		operator = "anonymous"
	}

	// Resolve this single flag first.
	if err := a.repo.Resolve(r.Context(), id, operator, req.Action); err != nil {
		writeError(w, http.StatusInternalServerError, "resolve_failed", err.Error())
		return
	}

	resp := map[string]any{
		"ok":     true,
		"id":     id,
		"action": req.Action,
		"note":   req.Note,
	}

	// ban_source: stop the source AND resolve every flag for the slug.
	// The source ID is supplied explicitly in the request body (the
	// canonical row in Manticore doesn't carry source_id today). When
	// SourceID is empty the action still resolves the lead flag and
	// bulk-resolves the slug's remaining flags but skips the source
	// stop; the response signals this via banned_source_id == "".
	if req.Action == domain.FlagActionBanSource {
		if req.SourceID != "" {
			if err := a.sourceRepo.StopSource(r.Context(), req.SourceID, operator, time.Now().UTC()); err != nil {
				util.Log(r.Context()).WithError(err).
					WithField("source_id", req.SourceID).
					Warn("ban_source: source stop failed")
			} else {
				resp["banned_source_id"] = req.SourceID
			}
		}
		// Bulk-resolve the rest of the flags on this slug. Don't unwind
		// if it fails — the lead flag already shows the operator's
		// decision.
		n, err := a.repo.ResolveAllForOpportunity(r.Context(), flag.OpportunitySlug, operator, req.Action)
		if err != nil {
			util.Log(r.Context()).WithError(err).
				WithField("slug", flag.OpportunitySlug).
				Warn("ban_source: bulk resolve failed")
		}
		resp["bulk_resolved"] = n
	}

	logFlagAction(r, "resolve:"+string(req.Action), id, flag.OpportunitySlug)
	writeJSON(w, http.StatusOK, resp)
}

// handleTopFlagged is GET /admin/opportunities/top-flagged?limit=20.
// Returns the most-flagged opportunities (by unresolved flag count).
func (a *flagsAdmin) handleTopFlagged(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.repo.TopFlagged(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": rows,
		"count":   len(rows),
	})
}

// isUniqueViolation reports whether err looks like a Postgres unique-
// constraint violation. We don't depend on pgx error codes directly —
// the message string is stable enough across pgx 5.x versions.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key value") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "SQLSTATE 23505")
}

// logFlagAction structured-logs a flag-related action for audit.
func logFlagAction(r *http.Request, action, flagID, slug string) {
	util.Log(r.Context()).
		WithField("flag_id", flagID).
		WithField("slug", slug).
		WithField("action", action).
		WithField("operator_id", profileIDFromJWT(r)).
		Info("flag admin action")
}

// Compile-time assertion: jobsManticore implements the lookup
// interface. Catches accidental signature drift on jm.GetByID.
var _ jobsManticoreLookup = (*jobsManticore)(nil)
