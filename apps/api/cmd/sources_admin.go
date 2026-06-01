// apps/api/cmd/sources_admin.go
//
// Admin surface for source CRUD + verification lifecycle. The crawler
// owns the operational primitives (pause/enable, scheduler-tick,
// reachability probe); this file is the canonical CRUD endpoint set —
// it is what an operator UI calls when reviewing the discovered queue,
// onboarding a new careers page, or rejecting a flaky source.
//
// All endpoints are namespaced under /admin/sources and gated by a
// Bearer-token middleware that gates handlers on the JWT having the
// 'admin' role. A gateway-side SecurityManager already verifies
// signatures and writes the claims into the context, so this middleware
// reads security.ClaimsFromContext, checks GetRoles() for 'admin', and
// returns 401 (no Bearer / no claims) or 403 (no admin role) otherwise.
// The JS auth-runtime exposes getRoles() that parses the same JWT
// shape, so the React app self-gates symmetrically — but requireAdmin
// IS the security boundary; the UI check is defense-in-depth.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	frameclient "github.com/pitabwire/frame/client"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/security"
	"github.com/pitabwire/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/arbeitnow"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/greenhouse"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/himalayas"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/jobicy"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/remoteok"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/sitemapcrawler"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/smartrecruiters"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/themuse"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/workday"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/freshness"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/sourceverify"
)

// sourceAdminRepo is the narrow slice of repository.SourceRepository the
// admin handlers use. Pulled into an interface so handler-level tests
// can run against an in-memory fake without standing up Postgres.
type sourceAdminRepo interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
	Create(ctx context.Context, s *domain.Source) error
	Update(ctx context.Context, id string, fields map[string]any) error
	HardDelete(ctx context.Context, id string) error
	DisableSource(ctx context.Context, id string) error
	PauseSource(ctx context.Context, id string) error
	EnableSource(ctx context.Context, id string) error
	StopSource(ctx context.Context, id, operator string, at time.Time) error
	StartSource(ctx context.Context, id string) error
	Approve(ctx context.Context, id, operator string, at time.Time) error
	Reject(ctx context.Context, id, reason string) error
	ListWithFilters(ctx context.Context, f repository.ListFilter) ([]*domain.Source, int64, error)
	ListByStatuses(ctx context.Context, statuses []domain.SourceStatus, limit int) ([]*domain.Source, error)

	// Adaptive recrawl surface (Plan D3). Used by the rescore handler
	// to force-recompute a source's score without waiting for the next
	// scheduler tick.
	LoadSignals(ctx context.Context) (map[string]freshness.SourceSignals, error)
	UpdateScoreAndNextCrawl(ctx context.Context, sourceID string, score float64, nextCrawlAt time.Time) error
}

// adminVerifier is the narrow slice of sourceverify.Dispatcher used by
// handlers. Lifted to an interface so tests can stub the verifier.
type adminVerifier interface {
	VerifyAndPersist(ctx context.Context, sourceID string) (*domain.VerificationReport, error)
}

// sourcesAdmin bundles the dependencies the admin handlers need.
type sourcesAdmin struct {
	repo       sourceAdminRepo
	dispatcher adminVerifier
	registry   *opportunity.Registry
}

// registerSourcesAdmin opens the database via Frame, builds the verifier
// + handlers, and wires every /admin/sources/* route on the supplied
// mux. Auth (Bearer token) is applied per-route via requireAdmin.
//
// Failures during init are surfaced as warnings so the api keeps
// serving public traffic even when the admin surface is misconfigured.
// registerSourcesAdmin signature gained an optional loader so main.go can
// pass the shared *definitions.R2Loader rather than this function spinning
// up its own (which would double-poll R2 and ignore broadcast events).
// nil means R2 isn't configured — the /admin/definitions/* block stays
// disabled in that case.
func registerSourcesAdmin(ctx context.Context, mux *http.ServeMux, cfg *apiConfig, reg *opportunity.Registry, loader *definitions.R2Loader) {
	log := util.Log(ctx)

	fc, err := fconfig.FromEnv[fconfig.ConfigurationDefault]()
	if err != nil {
		log.WithError(err).Warn("source admin: frame config parse failed; admin endpoints disabled")
		return
	}

	// Build a Frame service purely for its datastore manager. We don't
	// actually run the service (svc.Run); we just borrow the connection
	// pool so the same env vars (POSTGRES_*) work as in crawler.
	_, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&fc), frame.WithDatastore())

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Warn("source admin: no datastore pool; admin endpoints disabled")
		return
	}
	repo := repository.NewSourceRepository(pool.DB)

	// Frame-managed HTTP client (OTEL trace propagation + retry hooks).
	// Both the connector retry path and the verifier's raw HEAD/GET probes
	// share the same underlying client so admin verification feels
	// consistent with the timeout budget.
	verifyClient := frameclient.NewHTTPClient(ctx,
		frameclient.WithHTTPTimeout(time.Duration(cfg.HTTPTimeoutSec)*time.Second),
		frameclient.WithHTTPTraceRequests(),
	)
	connClient := httpx.NewClientFromDoer(verifyClient, cfg.UserAgent)
	connReg := buildAdminConnectorRegistry(connClient)
	verifier := sourceverify.NewVerifier(sourceverify.Config{
		HTTP:       verifyClient,
		Connectors: connReg,
		Registry:   reg,
		Robots:     sourceverify.NewRobotsHTTPChecker(verifyClient, 5*time.Second),
		UserAgent:  cfg.UserAgent,
	})
	// Wire the dispatcher onto Frame's workerpool. svc.WorkManager() is
	// the same pool that backs the queue subscribers, giving us bounded
	// parallelism for async source verification.
	dispatcher := sourceverify.NewDispatcher(verifier, repo, svc.WorkManager())

	_ = verifier // retained for future direct invocation; dispatcher wraps it for now.
	a := &sourcesAdmin{
		repo:       repo,
		dispatcher: dispatcher,
		registry:   reg,
	}

	// Order matters: more specific patterns first.
	mux.HandleFunc("GET /admin/sources/discovered", requireAdmin(a.handleListDiscovered))
	mux.HandleFunc("POST /admin/sources/{id}/verify", requireAdmin(a.handleVerify))
	mux.HandleFunc("POST /admin/sources/{id}/approve", requireAdmin(a.handleApprove))
	mux.HandleFunc("POST /admin/sources/{id}/reject", requireAdmin(a.handleReject))
	mux.HandleFunc("POST /admin/sources/{id}/pause", requireAdmin(a.handlePause))
	mux.HandleFunc("POST /admin/sources/{id}/resume", requireAdmin(a.handleResume))
	mux.HandleFunc("POST /admin/sources/{id}/stop", requireAdmin(a.handleStop))
	mux.HandleFunc("POST /admin/sources/{id}/start", requireAdmin(a.handleStart))
	mux.HandleFunc("POST /admin/sources/{id}/rescore", requireAdmin(a.handleRescore))
	mux.HandleFunc("GET /admin/sources/{id}", requireAdmin(a.handleGet))
	mux.HandleFunc("PUT /admin/sources/{id}", requireAdmin(a.handleUpdate))
	mux.HandleFunc("DELETE /admin/sources/{id}", requireAdmin(a.handleDelete))
	mux.HandleFunc("GET /admin/sources", requireAdmin(a.handleList))
	mux.HandleFunc("POST /admin/sources", requireAdmin(a.handleCreate))

	// /admin/trace/* shares the same database pool + source repo for
	// the source-lookup leg, so we wire it here rather than duplicating
	// the frame-datastore boot in main.go. The Iceberg catalog is
	// optional — when ICEBERG_CATALOG_URI isn't set the handler skips
	// the historic layer and serves Postgres-only summaries.
	var iceCat *icebergclient.Catalog
	if cat, err := icebergclient.NewCatalogFromEnv(ctx); err == nil {
		iceCat = cat
		log.Info("source admin: iceberg catalog wired for historic trace queries")
	} else {
		log.WithError(err).Warn("source admin: iceberg catalog init failed; historic queries disabled")
	}
	traceRepo := repository.NewTraceRepository(pool.DB)
	registerTraceAdmin(mux, traceRepo, repo, iceCat)
	registerDigestAdmin(mux, traceRepo, iceCat)

	// /admin/checkpoints — operator can inspect resume state per
	// source and force-clear a stuck checkpoint when a listing has
	// shifted and the source keeps resuming from a no-longer-fresh
	// page. Same database pool as the rest of the admin surface.
	checkpointRepo := repository.NewCheckpointRepository(pool.DB)
	registerCheckpointAdmin(mux, checkpointRepo)
	log.Info("source admin: /admin/checkpoints (GET/DELETE) wired")

	// /admin/frontier — operator surface for the D2 URL frontier.
	// Reuses the same Postgres pool. Read + requeue + delete only;
	// the worker side (enqueue / dequeue) is its own service.
	frontierRepo := frontier.NewAdminRepository(pool.DB)
	registerFrontierAdmin(mux, frontierRepo)
	log.Info("source admin: /admin/frontier wired")

	// /admin/raw_payloads/{id}/body — operator pulls the original HTML
	// from R2 for rejection drill-down. Wired only when R2 creds + the
	// archive bucket name are present; otherwise the endpoint stays
	// disabled and the rest of the admin surface keeps working.
	crawlRepo := repository.NewCrawlRepository(pool.DB)
	if cfg.R2AccountID != "" && cfg.R2ArchiveBucket != "" {
		arch := archive.NewR2Archive(archive.R2Config{
			AccountID:       cfg.R2AccountID,
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretAccessKey,
			Bucket:          cfg.R2ArchiveBucket,
		})
		registerRawPayloadAdmin(mux, crawlRepo, arch)
		log.Info("source admin: /admin/raw_payloads/{id}/body wired")
	} else {
		log.Warn("source admin: R2 not configured; /admin/raw_payloads/{id}/body disabled")
	}

	// /admin/raw_payloads/{id}/reparse + /admin/sources/{id}/reparse —
	// publish a CrawlRequestV1 with RawPayloadID set so the crawler
	// re-runs extraction on stored HTML. Available even without R2
	// configured (the crawler-side handler does the storage probe).
	registerReparseAdmin(mux, crawlRepo, frameEmitter{svc: svc})
	log.Info("source admin: /admin/raw_payloads/{id}/reparse + /admin/sources/{id}/reparse wired")

	// /admin/definitions/* — pluggable-definitions CRUD over R2. Reuses
	// the loader main.go already started so this surface, the per-replica
	// in-memory cache, and the broadcast subscriber all share one cache
	// (avoids double-polling R2 and ignoring NATS events). The S3 client
	// used for PutObject / DeleteObject is constructed here on the same
	// credentials.
	if loader != nil && cfg.R2AccountID != "" && cfg.R2ContentBucket != "" {
		s3Client := awss3.New(awss3.Options{
			Region:       "auto",
			Credentials:  awscreds.NewStaticCredentialsProvider(cfg.R2AccessKeyID, cfg.R2SecretAccessKey, ""),
			BaseEndpoint: aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.R2AccountID)),
		})
		registerDefinitionsAdmin(mux, loader, s3Client, cfg.R2ContentBucket, "definitions", frameEmitter{svc: svc})
		log.Info("source admin: /admin/definitions/* wired (shared loader)")
	} else {
		log.Warn("source admin: R2 not configured; /admin/definitions/* disabled")
	}

	log.Info("source admin: endpoints registered under /admin/sources, /admin/trace, /admin/definitions")
}

// buildAdminConnectorRegistry returns a connectors.Registry suitable for
// the verifier. It registers every API connector unconditionally; HTML
// connectors that need the AI extractor are skipped since the api does
// not have an LLM wired (verification will record SampleExtracted=false
// for those source types — operators can still review the rest of the
// report and approve manually).
func buildAdminConnectorRegistry(client *httpx.Client) *connectors.Registry {
	reg := connectors.NewRegistry()
	reg.Register(remoteok.New())
	reg.Register(arbeitnow.New())
	reg.Register(jobicy.New())
	reg.Register(themuse.New())
	reg.Register(himalayas.New())
	reg.Register(greenhouse.New(client))
	reg.Register(workday.New(client))
	reg.Register(smartrecruiters.New(client))
	reg.Register(sitemapcrawler.New(client))
	return reg
}

// ─────────────────────────────────────────────────────────────────────
// Handlers
// ─────────────────────────────────────────────────────────────────────

func (a *sourcesAdmin) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := repository.ListFilter{
		Status:  domain.SourceStatus(q.Get("status")),
		Kind:    q.Get("kind"),
		Type:    domain.SourceType(q.Get("type")),
		Country: q.Get("country"),
		Limit:   limit,
		Offset:  offset,
	}
	if filter.Status != "" && !domain.IsKnownSourceStatus(filter.Status) {
		writeError(w, http.StatusBadRequest, "invalid_status",
			fmt.Sprintf("unknown status %q", filter.Status))
		return
	}

	sources, total, err := a.repo.ListWithFilters(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": sources,
		"total":   total,
		"limit":   filter.Limit,
		"offset":  filter.Offset,
	})
}

func (a *sourcesAdmin) handleListDiscovered(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	sources, err := a.repo.ListByStatuses(r.Context(),
		[]domain.SourceStatus{domain.SourcePending, domain.SourceVerifying, domain.SourceVerified, domain.SourceRejected},
		limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources, "count": len(sources)})
}

func (a *sourcesAdmin) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	writeJSON(w, http.StatusOK, src)
}

// createSourceRequest is the shape clients send to POST /admin/sources.
// All fields are optional except Type, BaseURL, and Kinds — the
// handler defaults the rest to sensible values that mirror the
// discovery flow.
type createSourceRequest struct {
	Type                     domain.SourceType   `json:"type"`
	Name                     string              `json:"name"`
	BaseURL                  string              `json:"base_url"`
	Country                  string              `json:"country"`
	Language                 string              `json:"language"`
	Priority                 *domain.Priority    `json:"priority"`
	CrawlIntervalSec         int                 `json:"crawl_interval_sec"`
	Kinds                    []string            `json:"kinds"`
	RequiredAttributesByKind map[string][]string `json:"required_attributes_by_kind"`
	AutoApprove              *bool               `json:"auto_approve"`
}

func (a *sourcesAdmin) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.Type == "" || req.BaseURL == "" {
		writeError(w, http.StatusBadRequest, "missing_field", "type and base_url are required")
		return
	}
	if len(req.Kinds) == 0 {
		req.Kinds = []string{"job"}
	}
	for _, k := range req.Kinds {
		if a.registry != nil {
			if _, ok := a.registry.Lookup(k); !ok {
				writeError(w, http.StatusBadRequest, "unknown_kind",
					fmt.Sprintf("kind %q not in registry (known: %v)", k, a.registry.Known()))
				return
			}
		}
	}
	if sourceverify.IsBlockedURL(req.BaseURL) {
		writeError(w, http.StatusBadRequest, "blocked_url",
			"base_url host is on the platform blocklist")
		return
	}

	// Default is false: operator-created sources walk through
	// pending → verifying → verified and wait for an explicit
	// POST /admin/sources/{id}/approve. The operator can opt in to
	// auto-promotion by setting auto_approve=true on creation.
	autoApprove := false
	if req.AutoApprove != nil {
		autoApprove = *req.AutoApprove
	}
	priority := domain.PriorityNormal
	if req.Priority != nil {
		priority = *req.Priority
	}
	interval := req.CrawlIntervalSec
	if interval <= 0 {
		interval = 3600
	}
	lang := req.Language
	if lang == "" {
		lang = "en"
	}

	src := &domain.Source{
		Type:                     req.Type,
		Name:                     req.Name,
		BaseURL:                  req.BaseURL,
		Country:                  req.Country,
		Language:                 lang,
		Status:                   domain.SourcePending,
		Priority:                 priority,
		CrawlIntervalSec:         interval,
		HealthScore:              1.0,
		Config:                   "{}",
		Kinds:                    req.Kinds,
		RequiredAttributesByKind: req.RequiredAttributesByKind,
		AutoApprove:              autoApprove,
	}
	if src.RequiredAttributesByKind == nil {
		src.RequiredAttributesByKind = map[string][]string{}
	}
	if err := a.repo.Create(r.Context(), src); err != nil {
		writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	logAction(r, "create", src.ID)

	writeJSON(w, http.StatusCreated, src)
}

// updateSourceRequest enumerates the fields that can be edited via PUT.
// Status is intentionally NOT here — lifecycle transitions go through
// the dedicated endpoints (verify/approve/reject/pause/resume).
type updateSourceRequest struct {
	Name                      *string              `json:"name"`
	Country                   *string              `json:"country"`
	Language                  *string              `json:"language"`
	Priority                  *domain.Priority     `json:"priority"`
	CrawlIntervalSec          *int                 `json:"crawl_interval_sec"`
	Kinds                     *[]string            `json:"kinds"`
	RequiredAttributesByKind  *map[string][]string `json:"required_attributes_by_kind"`
	AutoApprove               *bool                `json:"auto_approve"`
	ExtractionPromptExtension *string              `json:"extraction_prompt_extension"`
	// FrontierEnabled is the D2 opt-in flag — flips the source's
	// crawl path to the URL-frontier model. Default (unset) leaves
	// the existing value alone; legacy direct-extract sources
	// stay on the legacy path until the operator explicitly opts in.
	FrontierEnabled *bool `json:"frontier_enabled"`
}

func (a *sourcesAdmin) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}

	var req updateSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}

	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Country != nil {
		updates["country"] = *req.Country
	}
	if req.Language != nil {
		updates["language"] = *req.Language
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.CrawlIntervalSec != nil {
		if *req.CrawlIntervalSec < 60 {
			writeError(w, http.StatusBadRequest, "invalid_interval", "crawl_interval_sec must be >= 60")
			return
		}
		updates["crawl_interval_sec"] = *req.CrawlIntervalSec
	}
	if req.Kinds != nil {
		for _, k := range *req.Kinds {
			if a.registry != nil {
				if _, ok := a.registry.Lookup(k); !ok {
					writeError(w, http.StatusBadRequest, "unknown_kind",
						fmt.Sprintf("kind %q not in registry", k))
					return
				}
			}
		}
		updates["kinds"] = *req.Kinds
	}
	if req.RequiredAttributesByKind != nil {
		updates["required_attributes_by_kind"] = *req.RequiredAttributesByKind
	}
	if req.AutoApprove != nil {
		updates["auto_approve"] = *req.AutoApprove
	}
	if req.ExtractionPromptExtension != nil {
		if len(*req.ExtractionPromptExtension) > 4096 {
			writeError(w, http.StatusBadRequest, "bad_request", "extraction_prompt_extension exceeds 4096 bytes")
			return
		}
		updates["extraction_prompt_extension"] = *req.ExtractionPromptExtension
	}
	if req.FrontierEnabled != nil {
		updates["frontier_enabled"] = *req.FrontierEnabled
	}
	if len(updates) == 0 {
		writeError(w, http.StatusBadRequest, "no_fields", "request had no editable fields")
		return
	}

	if err := a.repo.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	logAction(r, "update", id)

	updated, _ := a.repo.GetByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

func (a *sourcesAdmin) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hard := r.URL.Query().Get("hard") == "true"

	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}

	if hard {
		if err := a.repo.HardDelete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			return
		}
	} else {
		if err := a.repo.DisableSource(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "disable_failed", err.Error())
			return
		}
	}
	logAction(r, "delete", id)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "hard": hard})
}

func (a *sourcesAdmin) handleVerify(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rep, err := a.dispatcher.VerifyAndPersist(r.Context(), id)
	if err != nil {
		if errors.Is(err, sourceverify.ErrNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, "not_configured", err.Error())
			return
		}
		if rep == nil {
			writeError(w, http.StatusInternalServerError, "verify_failed", err.Error())
			return
		}
		// Report exists but persistence failed — return both.
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":  map[string]any{"code": "persist_failed", "message": err.Error()},
			"report": rep,
		})
		return
	}
	logAction(r, "verify", id)
	writeJSON(w, http.StatusOK, rep)
}

func (a *sourcesAdmin) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	if src.Status != domain.SourceVerified {
		writeError(w, http.StatusConflict, "invalid_transition",
			fmt.Sprintf("source must be in %q to approve (current: %q)", domain.SourceVerified, src.Status))
		return
	}
	operator := profileIDFromJWT(r)
	if operator == "" {
		operator = "anonymous"
	}
	if err := a.repo.Approve(r.Context(), id, operator, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, "approve_failed", err.Error())
		return
	}
	logAction(r, "approve", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": domain.SourceActive})
}

// rejectRequest accepts the rejection reason in the body. The reason
// can also be supplied via ?reason= for shell-friendly invocations.
type rejectRequest struct {
	Reason string `json:"reason"`
}

func (a *sourcesAdmin) handleReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		var req rejectRequest
		_ = decodeJSON(r, &req) // best-effort; empty body is fine
		reason = req.Reason
	}
	if reason == "" {
		reason = "no reason given"
	}

	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	if err := a.repo.Reject(r.Context(), id, reason); err != nil {
		writeError(w, http.StatusInternalServerError, "reject_failed", err.Error())
		return
	}
	logAction(r, "reject", id)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"id":     id,
		"status": domain.SourceRejected,
		"reason": reason,
	})
}

func (a *sourcesAdmin) handlePause(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	if err := a.repo.PauseSource(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "pause_failed", err.Error())
		return
	}
	logAction(r, "pause", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": domain.SourcePaused})
}

func (a *sourcesAdmin) handleResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	if err := a.repo.EnableSource(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "resume_failed", err.Error())
		return
	}
	logAction(r, "resume", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": domain.SourceActive})
}

// handleStop is the operator's "kill switch": permanently disables a
// source while keeping it reversible via /start. Allowed from any
// non-terminal status (pending/verifying/verified/rejected/active/
// degraded/paused/blocked); records last_stopped_at + last_stopped_by
// for audit. Already-disabled sources return 409.
func (a *sourcesAdmin) handleStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	if src.Status == domain.SourceDisabled {
		writeError(w, http.StatusConflict, "already_stopped",
			"source is already stopped; use /start to revive")
		return
	}
	operator := profileIDFromJWT(r)
	if operator == "" {
		operator = "anonymous"
	}
	if err := a.repo.StopSource(r.Context(), id, operator, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, "stop_failed", err.Error())
		return
	}
	logAction(r, "stop", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": domain.SourceDisabled})
}

// handleStart reverses a stop. Requires current status == SourceDisabled
// — pause/resume covers transient holds, this path is specifically for
// reviving a stopped source.
func (a *sourcesAdmin) handleStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	if src.Status != domain.SourceDisabled {
		writeError(w, http.StatusConflict, "invalid_transition",
			fmt.Sprintf("source must be in %q to start (current: %q)", domain.SourceDisabled, src.Status))
		return
	}
	if err := a.repo.StartSource(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "start_failed", err.Error())
		return
	}
	logAction(r, "start", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": domain.SourceActive})
}

// handleRescore force-recomputes a single source's freshness score +
// next_crawl_at without waiting for the next scheduler tick. The
// signals come from the crawl_signals materialized view; if the view
// has no row for this source yet (e.g. brand new source pre-first-
// refresh) we return 404 so the operator knows to wait one tick or
// trigger a verification crawl first.
//
// Tier is hard-coded to 2 (neutral) until Source.Tier ships — see
// Plan B2 follow-up.
func (a *sourcesAdmin) handleRescore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := a.repo.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}
	signals, err := a.repo.LoadSignals(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_signals_failed", err.Error())
		return
	}
	sig, ok := signals[id]
	if !ok {
		writeError(w, http.StatusNotFound, "no_signals",
			"source has no crawl_signals row yet — wait for the next refresh or trigger a crawl first")
		return
	}
	now := time.Now().UTC()
	score := freshness.Score(sig, 2, now)
	minMin := src.MinIntervalMinutes
	if minMin <= 0 {
		minMin = 15
	}
	maxMin := src.MaxIntervalMinutes
	if maxMin <= 0 {
		maxMin = 10080
	}
	next := freshness.NextCrawlAt(score, now, minMin, maxMin)
	if err := a.repo.UpdateScoreAndNextCrawl(r.Context(), id, score, next); err != nil {
		writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	logAction(r, "rescore", id)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"id":            id,
		"score":         score,
		"next_crawl_at": next,
	})
}

// ─────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────

// requireAdmin gates the handler on (a) presence of a Bearer token and
// (b) the 'admin' role in the JWT claims. Frame's security middleware
// extracts claims upstream into the context via security.ClaimsToContext.
//
// On unauth (no Bearer, or upstream rejected the token leaving claims
// unset): 401. On forbidden (authenticated but not admin): 403.
//
// The JS client (@stawi/auth-runtime) exposes getRoles() that parses
// the same JWT shape, so the UI self-gates symmetrically — but this
// middleware IS the security boundary.
func requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing Bearer token")
			return
		}
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid Bearer token")
			return
		}
		if !containsRole(claims.GetRoles(), "admin") {
			writeError(w, http.StatusForbidden, "forbidden", "admin role required")
			return
		}
		h(w, r)
	}
}

// containsRole returns true if want is in roles. Case-sensitive
// because role names in Hydra are canonical.
func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// errorEnvelope is the shape every admin error response follows. Plays
// nicely with Connect-style clients that expect a uniform error object.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decodeJSON(r *http.Request, dst any) error {
	defer func() { _, _ = io.Copy(io.Discard, r.Body); _ = r.Body.Close() }()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// logAction records every state-changing admin call with the operator
// and source id. The standard structured-log fields make grep + log
// queries straightforward.
func logAction(r *http.Request, action, sourceID string) {
	util.Log(r.Context()).
		WithField("source_id", sourceID).
		WithField("action", action).
		WithField("operator_id", profileIDFromJWT(r)).
		Info("source admin action")
}
