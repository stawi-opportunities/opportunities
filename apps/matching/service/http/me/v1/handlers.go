package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/xid"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/placement"
	"github.com/stawi-opportunities/opportunities/pkg/profilecontacts"
)

// Deps bundles all dependencies injected into the handler set.
type Deps struct {
	DB               *sql.DB
	Matches          *matching.Store
	MatchEvents      *matching.EventLog
	Rules            *matching.RulesStore
	IndexStore       *matching.IndexStore
	KNN              *matching.KNN
	Reranker         matching.Reranker
	Weights          matching.Weights
	Debouncer        matching.Debouncer
	IdempotencyStore *applications.IdempotencyStore
	// DailyCap enforces plan daily generation limits during gap-fill.
	DailyCap matching.DailyCapQuery
	// DefaultMinScore floors on-demand gap-fill when the index has no
	// per-candidate threshold (MATCHING_MIN_SCORE). 0 → 0.45.
	DefaultMinScore float64
	// Contacts creates standalone ProfileService contacts for CV details.
	// Checkout/notify use only profile-attached identity contacts (not these).
	Contacts profilecontacts.Directory
	// Placement rebuilds persona + embedding when the match index has no vector
	// (CV upload may have completed structure but embed lagged/failed).
	Placement *placement.Service

	Now   func() time.Time
	NewID func() string
}

// effectiveMinScore returns a usable 0–1 threshold.
func effectiveMinScore(indexScore, defaultScore float64) float64 {
	if indexScore > 0 && indexScore <= 1 {
		return indexScore
	}
	if defaultScore > 0 && defaultScore <= 1 {
		return defaultScore
	}
	return 0.45
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Deps) newID() string {
	if d.NewID != nil {
		return d.NewID()
	}
	return xid.New().String()
}

// ---- GET /api/me ----

type meResp struct {
	CandidateID      string `json:"candidate_id"`
	AutoApplyEnabled bool   `json:"autoapply_enabled"`
}

func meHandler(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		auto := false
		if d.Rules != nil {
			if rr, err := d.Rules.Get(r.Context(), cand); err == nil {
				auto = rr.Document.Autoapply.Enabled
			}
		}
		// Profile-level auto_apply (set on paid Pro/Managed activation)
		// is the entitlement gate; rules toggle is the user preference.
		if !auto && d.DB != nil {
			var profileAuto bool
			if err := d.DB.QueryRowContext(r.Context(),
				`SELECT COALESCE(auto_apply, false) FROM candidate_profiles WHERE id = $1`, cand,
			).Scan(&profileAuto); err == nil {
				auto = profileAuto
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meResp{CandidateID: cand, AutoApplyEnabled: auto})
	}
}

// ---- GET /api/me/matches ----

type matchResp struct {
	MatchID       string         `json:"match_id"`
	OpportunityID string         `json:"opportunity_id"`
	ApplyURL      string         `json:"apply_url"`
	Status        string         `json:"status"`
	Score         float64        `json:"score"`
	RerankScore   *float64       `json:"rerank_score,omitempty"`
	ViewedAt      *time.Time     `json:"viewed_at,omitempty"`
	DismissedAt   *time.Time     `json:"dismissed_at,omitempty"`
	AppliedAt     *time.Time     `json:"applied_at,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

func toMatchResp(m matching.Match) matchResp {
	return matchResp{
		MatchID: m.MatchID, OpportunityID: m.OpportunityID, ApplyURL: m.ApplyURL,
		Status: string(m.Status), Score: m.Score, RerankScore: m.RerankScore,
		ViewedAt: m.ViewedAt, DismissedAt: m.DismissedAt, AppliedAt: m.AppliedAt,
		Metadata: m.Metadata, CreatedAt: m.CreatedAt,
	}
}

func listMatches(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		statuses := parseStatuses(r.URL.Query().Get("status"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, err := d.Matches.ListByCandidate(r.Context(), matching.ListByCandidateParams{
			CandidateID: cand,
			Statuses:    statuses,
			Cursor:      r.URL.Query().Get("cursor"),
			Limit:       limit,
		})
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		items := make([]matchResp, 0, len(page.Items))
		for _, m := range page.Items {
			items = append(items, toMatchResp(m))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": items, "next_cursor": page.NextCursor, "has_more": page.HasMore,
		})
	}
}

func parseStatuses(raw string) []matching.MatchStatus {
	if raw == "" {
		return nil
	}
	out := []matching.MatchStatus{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, matching.MatchStatus(s))
		}
	}
	return out
}

// ---- GET /api/me/matches/{match_id} ----

func getMatch(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		id := r.PathValue("match_id")
		m, err := loadOwnedMatch(r.Context(), d, cand, id)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toMatchResp(*m))
	}
}

// loadOwnedMatch resolves the match by ID and verifies candidate
// ownership. Returns matching.ErrNotFound if the row doesn't exist or
// is owned by someone else.
func loadOwnedMatch(ctx context.Context, d *Deps, candidateID, matchID string) (*matching.Match, error) {
	row := d.DB.QueryRowContext(ctx, `
SELECT match_id, candidate_id, opportunity_id,
       COALESCE((SELECT o.apply_url FROM opportunities o WHERE o.canonical_id=candidate_matches.opportunity_id), ''),
       status, score, rerank_score,
       reranker_used, viewed_at, applied_at, dismissed_at,
       COALESCE(last_event_id,''), metadata, created_at, updated_at
FROM candidate_matches
WHERE match_id = $1 AND candidate_id = $2
`, matchID, candidateID)
	var (
		m         matching.Match
		status    string
		rerank    sql.NullFloat64
		viewedAt  sql.NullTime
		appliedAt sql.NullTime
		dismAt    sql.NullTime
		mdRaw     []byte
	)
	if err := row.Scan(&m.MatchID, &m.CandidateID, &m.OpportunityID, &m.ApplyURL, &status,
		&m.Score, &rerank, &m.RerankerUsed,
		&viewedAt, &appliedAt, &dismAt, &m.LastEventID, &mdRaw,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, matching.ErrNotFound
		}
		return nil, err
	}
	m.Status = matching.MatchStatus(status)
	if rerank.Valid {
		v := rerank.Float64
		m.RerankScore = &v
	}
	if viewedAt.Valid {
		t := viewedAt.Time
		m.ViewedAt = &t
	}
	if appliedAt.Valid {
		t := appliedAt.Time
		m.AppliedAt = &t
	}
	if dismAt.Valid {
		t := dismAt.Time
		m.DismissedAt = &t
	}
	if len(mdRaw) > 0 {
		_ = json.Unmarshal(mdRaw, &m.Metadata)
	}
	return &m, nil
}

// ---- POST /api/me/matches/{match_id}/dismiss ----

// DismissMatchHandler is the exported form used for the gateway-visible
// /me/matches/{match_id}/dismiss alias in main.
func DismissMatchHandler(d *Deps) http.HandlerFunc {
	return dismissMatch(d)
}

func dismissMatch(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		id := r.PathValue("match_id")
		m, err := loadOwnedMatch(r.Context(), d, cand, id)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		// Idempotent: if already dismissed, return the row unchanged.
		if m.Status == matching.StatusDismissed {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(toMatchResp(*m))
			return
		}
		_, err = d.DB.ExecContext(r.Context(),
			`UPDATE candidate_matches
			    SET status = 'dismissed', dismissed_at = now(), updated_at = now()
			  WHERE match_id = $1 AND candidate_id = $2`, id, cand)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		evtID := d.newID()
		_ = d.MatchEvents.WriteMatchEvent(r.Context(), matching.MatchEvent{
			EventID:       evtID,
			OccurredAt:    d.now(),
			CandidateID:   cand,
			OpportunityID: m.OpportunityID,
			Kind:          matching.EventKindDismissed,
			Path:          matching.PathCandidateChange,
			Score:         m.Score,
			Data:          map[string]any{"match_id": id, "source": "user"},
		})
		// Re-read so the response reflects the new state.
		m2, _ := loadOwnedMatch(r.Context(), d, cand, id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toMatchResp(*m2))
	}
}

// ---- POST /api/me/matches/{match_id}/view ----

func viewMatch(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		id := r.PathValue("match_id")
		m, err := loadOwnedMatch(r.Context(), d, cand, id)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		_, _ = d.DB.ExecContext(r.Context(),
			`UPDATE candidate_matches
			    SET viewed_at = COALESCE(viewed_at, now()), updated_at = now()
			  WHERE match_id = $1 AND candidate_id = $2`, id, cand)
		// engagement_events beacon
		evtID := d.newID()
		_, _ = d.DB.ExecContext(r.Context(), `
INSERT INTO engagement_events (event_id, occurred_at, candidate_id, opportunity_id, kind, source, data)
VALUES ($1, now(), $2, $3, 'view', 'extension', $4::jsonb)
ON CONFLICT (event_id, occurred_at) DO NOTHING
`, evtID, cand, m.OpportunityID, []byte(`{"match_id":"`+id+`"}`))
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- GET /api/me/rules ----

func getRules(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		rr, err := d.Rules.Get(r.Context(), cand)
		if err != nil {
			if errors.Is(err, matching.ErrNotFound) {
				// Default rules until the candidate explicitly PUTs.
				def := applications.DefaultRules()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(def)
				return
			}
			ProblemFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rr.Document)
	}
}

// ---- PUT /api/me/rules ----

func putRules(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		const maxBodyBytes = 64 * 1024
		data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_input", "unreadable body")
			return
		}
		if int64(len(data)) > maxBodyBytes {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "bad_input", "body too large")
			return
		}
		rules, err := applications.ParseRules(data)
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_rules", err.Error())
			return
		}
		// Auto-apply requires a paid Pro/Managed entitlement on the profile.
		if rules.Autoapply.Enabled && d.DB != nil {
			var autoOK bool
			_ = d.DB.QueryRowContext(r.Context(),
				`SELECT COALESCE(auto_apply, false) FROM candidate_profiles WHERE id = $1`, cand,
			).Scan(&autoOK)
			if !autoOK {
				httpmw.ProblemJSON(w, http.StatusPaymentRequired, "autoapply_not_entitled",
					"auto-apply requires a Pro or Managed subscription")
				return
			}
		}
		rr, err := d.Rules.Upsert(r.Context(), cand, rules)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		// Best-effort Path C trigger. Errors logged but not propagated —
		// the rules write succeeded and the change is committed.
		if d.IndexStore != nil && d.Debouncer != nil && d.KNN != nil {
			if idx, ierr := d.IndexStore.Get(r.Context(), cand); ierr == nil {
				_, _ = matching.RunCandidateChange(r.Context(), matching.CandidateChange{
					CandidateID:    cand,
					Embedding:      idx.Embedding,
					Countries:      idx.Countries,
					Kinds:          idx.Kinds,
					SalaryFloorUSD: idx.SalaryFloorUSD,
					MinScore:       rules.MinScore,
					TriggeredBy:    "rules_changed",
				}, matching.CandidateChangeDeps{
					Debouncer: d.Debouncer,
					GapFill: matching.GapFillDeps{
						KNN: d.KNN, Store: d.Matches, EventLog: d.MatchEvents,
						Reranker: d.Reranker, Weights: d.Weights,
					},
				})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rr.Document)
	}
}

// ---- POST /api/me/matches/refresh (and /me/matches/refresh) ----
// On-demand gap-fill for a paid active candidate so the dashboard can
// collect matches immediately after payment / CV upload without waiting
// for the Monday Trustage digest.

// RefreshMatchesHandler is the exported form used for the gateway-visible
// /me/matches/refresh alias in main.
func RefreshMatchesHandler(d *Deps) http.HandlerFunc {
	return refreshMatches(d)
}

func refreshMatches(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		// JWT sub is platform profile_id. Match index / matches may be keyed
		// by product candidate id or profile_id (legacy dual-key era).
		profileID := httpmw.ProfileIDFromContext(ctx)
		if d.DB == nil || d.IndexStore == nil || d.KNN == nil || d.Matches == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "matching_unavailable", "match pipeline not configured")
			return
		}

		matchKey, sub, planID, err := resolveMatchIdentity(ctx, d.DB, profileID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", "profile not found")
				return
			}
			ProblemFromError(w, err)
			return
		}
		paid := false
		switch strings.ToLower(strings.TrimSpace(sub)) {
		case "paid", "past_due", "trial":
			paid = true
		}

		idx, err := loadMatchIndex(ctx, d.IndexStore, profileID, matchKey)
		if err != nil && !errors.Is(err, matching.ErrNotFound) {
			ProblemFromError(w, err)
			return
		}
		if idx == nil || len(idx.Embedding) == 0 {
			// Build embedding now from stored CV/placement — do not 409 if we
			// have material. Async embed after upload can lag or fail quietly.
			built, bErr := ensureMatchEmbedding(ctx, d, profileID, matchKey)
			if bErr != nil {
				util.Log(ctx).WithError(bErr).WithField("profile_id", profileID).
					WithField("match_key", matchKey).
					Warn("matches/refresh: ensure embedding failed")
			}
			if built != nil {
				idx = built
			}
		}
		if idx == nil || len(idx.Embedding) == 0 {
			httpmw.ProblemJSON(w, http.StatusConflict, "no_embedding",
				"no CV embedding available — upload a CV under Dashboard → CV, then try again")
			return
		}
		// Prefer the index's stored candidate key for gap-fill writes.
		gapKey := strings.TrimSpace(idx.CandidateID)
		if gapKey == "" {
			gapKey = matchKey
		}

		minScore := effectiveMinScore(idx.MinScore, d.DefaultMinScore)
		// Free proof: wider lookback so first match is useful; tight caps.
		since := time.Now().UTC().Add(-30 * 24 * time.Hour)
		dailyCap, weeklyCap := idx.DailyCap, idx.WeeklyCap
		if !paid {
			since = time.Now().UTC().Add(-90 * 24 * time.Hour)
			ent := billing.EntitlementsFor(billing.PlanID(planID))
			// Empty plan → free proof caps (not starter).
			if strings.TrimSpace(planID) == "" || strings.EqualFold(sub, "free") || sub == "" {
				ent = billing.EntitlementsFor("")
			}
			dailyCap, weeklyCap = ent.DailyCap, ent.WeeklyCap
		}
		res, runErr := matching.GapFill(ctx, matching.GapFillInput{
			CandidateID:    gapKey,
			Embedding:      idx.Embedding,
			Countries:      idx.Countries,
			Kinds:          idx.Kinds,
			SalaryFloorUSD: idx.SalaryFloorUSD,
			Since:          since,
			MinScore:       minScore,
			DailyCap:       dailyCap,
			WeeklyCap:      weeklyCap,
		}, matching.GapFillDeps{
			KNN:       d.KNN,
			Store:     d.Matches,
			EventLog:  d.MatchEvents,
			Reranker:  d.Reranker,
			Weights:   d.Weights,
			DailyCap:  d.DailyCap,
			WeekCount: d.Matches,
		})
		if runErr != nil {
			ProblemFromError(w, runErr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"matches_written":  res.MatchesWritten,
			"opps_scanned":     res.OppsScanned,
			"scored_above_min": res.ScoredAboveMin,
			"run_id":           res.RunID,
			"min_score":        minScore,
			"reason":           res.Reason,
			"weekly_used":      res.WeeklyUsed,
			"weekly_cap":       res.WeeklyCap,
			"daily_cap":        dailyCap,
			"proof":            !paid,
		})
	}
}

// resolveMatchIdentity maps JWT profile_id → product candidate id + subscription.
func resolveMatchIdentity(ctx context.Context, db *sql.DB, profileID string) (matchKey, sub, planID string, err error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "", "", "", sql.ErrNoRows
	}
	var id string
	err = db.QueryRowContext(ctx, `
SELECT id, COALESCE(subscription,''), COALESCE(plan_id,'')
  FROM candidate_profiles
 WHERE profile_id = $1 OR id = $1
 ORDER BY CASE WHEN profile_id = $1 THEN 0 ELSE 1 END
 LIMIT 1`, profileID).Scan(&id, &sub, &planID)
	if err != nil {
		return "", "", "", err
	}
	// Prefer product-local candidate id for match FKs; fall back to profile_id.
	matchKey = strings.TrimSpace(id)
	if matchKey == "" {
		matchKey = profileID
	}
	return matchKey, sub, planID, nil
}

// loadMatchIndex tries profile_id then candidate id (dual-key safety).
func loadMatchIndex(ctx context.Context, store *matching.IndexStore, profileID, candidateID string) (*matching.CandidateIndex, error) {
	if store == nil {
		return nil, matching.ErrNotFound
	}
	seen := map[string]struct{}{}
	for _, key := range []string{candidateID, profileID} {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		idx, err := store.Get(ctx, key)
		if err == nil && idx != nil && len(idx.Embedding) > 0 {
			return idx, nil
		}
		if err != nil && !errors.Is(err, matching.ErrNotFound) {
			return nil, err
		}
		// Keep a non-empty row without embedding in case ensure fails later.
		if err == nil && idx != nil {
			return idx, nil
		}
	}
	return nil, matching.ErrNotFound
}

// ensureMatchEmbedding rebuilds placement persona + vector when missing.
// Uses CV/placement text already on the profile; fails only when there is
// nothing meaningful to embed.
func ensureMatchEmbedding(ctx context.Context, d *Deps, profileID, matchKey string) (*matching.CandidateIndex, error) {
	if d == nil || d.Placement == nil {
		return nil, errors.New("placement service not configured")
	}
	fields, err := loadPlacementFieldsForMatch(ctx, d, profileID, matchKey)
	if err != nil {
		return nil, err
	}
	// Need at least CV corpus or a target role to embed.
	if strings.TrimSpace(fields.ExtraInfo) == "" && strings.TrimSpace(fields.TargetJobTitle) == "" {
		return nil, errors.New("no CV or target role to embed")
	}
	// Rebuild under both keys when they differ so subsequent lookups hit.
	keys := uniqueNonEmpty(matchKey, profileID)
	var last *placement.RebuildResult
	for _, key := range keys {
		res, rErr := d.Placement.Rebuild(ctx, placement.RebuildInput{
			CandidateID: key,
			Fields:      fields,
		})
		if rErr != nil {
			return nil, rErr
		}
		last = res
	}
	if last == nil || !last.Embedded {
		// Rebuild may store summary without vector when embedder is down.
		return loadMatchIndex(ctx, d.IndexStore, profileID, matchKey)
	}
	return loadMatchIndex(ctx, d.IndexStore, profileID, matchKey)
}

func loadPlacementFieldsForMatch(ctx context.Context, d *Deps, profileID, matchKey string) (placement.Fields, error) {
	var f placement.Fields
	// Prefer stored placement qualifications (full CV corpus).
	if d.Placement != nil && d.Placement.Store != nil {
		for _, key := range uniqueNonEmpty(matchKey, profileID) {
			doc, err := d.Placement.Store.Get(ctx, key)
			if err != nil || doc == nil {
				continue
			}
			q := strings.TrimSpace(strings.TrimPrefix(doc.QualificationsText, "## Qualifications"))
			q = strings.TrimSpace(q)
			if q != "" && q != "(CV not yet provided)" {
				f.ExtraInfo = q
			}
			if f.ExtraInfo != "" {
				break
			}
		}
	}
	// Overlay structured profile-fields (role, countries, skills as ExtraInfo fallback).
	if d.DB != nil {
		for _, key := range uniqueNonEmpty(matchKey, profileID) {
			pf, _, err := candidatestore.GetProfileFields(ctx, d.DB, key)
			if err != nil || pf == nil {
				continue
			}
			if f.TargetJobTitle == "" {
				f.TargetJobTitle = firstNonEmpty(pf.TargetJobTitle, pf.CurrentTitle)
			}
			if f.ExperienceLevel == "" {
				f.ExperienceLevel = firstNonEmpty(pf.ExperienceLevel, pf.Seniority)
			}
			if len(f.PreferredCountries) == 0 {
				f.PreferredCountries = append([]string(nil), pf.Countries...)
			}
			if len(f.PreferredRegions) == 0 {
				f.PreferredRegions = append([]string(nil), pf.Regions...)
			}
			if len(f.JobTypes) == 0 && len(pf.PreferredRoles) > 0 {
				f.JobTypes = append([]string(nil), pf.PreferredRoles...)
			}
			if f.ExtraInfo == "" {
				// Synthesize a short corpus from skills/title when no CV text.
				var parts []string
				if t := firstNonEmpty(pf.CurrentTitle, pf.TargetJobTitle); t != "" {
					parts = append(parts, t)
				}
				if len(pf.StrongSkills) > 0 {
					parts = append(parts, "skills: "+strings.Join(pf.StrongSkills, ", "))
				} else if len(pf.Skills) > 0 {
					parts = append(parts, "skills: "+strings.Join(pf.Skills, ", "))
				}
				if b := strings.TrimSpace(pf.Bio); b != "" {
					parts = append(parts, b)
				}
				if len(parts) > 0 {
					f.ExtraInfo = strings.Join(parts, ". ")
				}
			}
			break
		}
	}
	return f, nil
}

func uniqueNonEmpty(vals ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// ---- GET/PUT /api/me/notifications (and /me/notifications) ----

type notificationPrefsResp struct {
	EmailDigest     string `json:"email_digest"`
	MatchAlerts     bool   `json:"match_alerts"`
	WeeklySummary   bool   `json:"weekly_summary"`
	MarketingEmails bool   `json:"marketing_emails"`
}

// NotificationsHandler serves GET+PUT for gateway-visible /me/notifications.
func NotificationsHandler(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getNotifications(d)(w, r)
		case http.MethodPut:
			putNotifications(d)(w, r)
		default:
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
		}
	}
}

func getNotifications(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		if d.DB == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "unavailable", "database not configured")
			return
		}
		var (
			digest   string
			matchAl  bool
			weekly   bool
			market   bool
			commMail bool
		)
		err := d.DB.QueryRowContext(r.Context(), `
SELECT COALESCE(email_digest, 'weekly'),
       COALESCE(match_alerts, false),
       COALESCE(weekly_summary, true),
       COALESCE(marketing_emails, false),
       COALESCE(comm_email, true)
FROM candidate_profiles WHERE id = $1`, cand).Scan(&digest, &matchAl, &weekly, &market, &commMail)
		if errors.Is(err, sql.ErrNoRows) {
			// New account defaults — still return a valid prefs object.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(notificationPrefsResp{
				EmailDigest: matching.DigestWeekly, MatchAlerts: false, WeeklySummary: true,
			})
			return
		}
		if err != nil {
			// Columns may not exist yet on a lagging migrate — fall back to defaults.
			if strings.Contains(err.Error(), "email_digest") || strings.Contains(err.Error(), "does not exist") {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(notificationPrefsResp{
					EmailDigest: matching.DigestWeekly, MatchAlerts: false, WeeklySummary: true,
				})
				return
			}
			ProblemFromError(w, err)
			return
		}
		_ = commMail // channel reserved for multi-channel digests
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(notificationPrefsResp{
			EmailDigest:     matching.NormalizeDigestCadence(digest),
			MatchAlerts:     matchAl,
			WeeklySummary:   weekly,
			MarketingEmails: market,
		})
	}
}

func putNotifications(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		if d.DB == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "unavailable", "database not configured")
			return
		}
		var body notificationPrefsResp
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "invalid notification preferences body")
			return
		}
		digest := matching.NormalizeDigestCadence(body.EmailDigest)
		res, err := d.DB.ExecContext(r.Context(), `
UPDATE candidate_profiles SET
  email_digest = $2,
  match_alerts = $3,
  weekly_summary = $4,
  marketing_emails = $5,
  updated_at = NOW()
WHERE id = $1`, cand, digest, body.MatchAlerts, body.WeeklySummary, body.MarketingEmails)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", "profile not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"email_digest":     digest,
			"match_alerts":     body.MatchAlerts,
			"weekly_summary":   body.WeeklySummary,
			"marketing_emails": body.MarketingEmails,
		})
	}
}

// ---- GET+PUT /api/me/profile-fields ----

// ProfileFieldsHandler serves gateway-visible /me/profile-fields (GET+PUT).
func ProfileFieldsHandler(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			profileFields(d)(w, r)
		case http.MethodPut:
			putProfileFields(d)(w, r)
		default:
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
		}
	}
}

func profileFields(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		if d.DB == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "unavailable", "database not configured")
			return
		}
		pf, etag, err := candidatestore.GetProfileFields(r.Context(), d.DB, cand)
		if err != nil {
			if errors.Is(err, candidatestore.ErrProfileNotFound) {
				httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", "profile not found")
				return
			}
			ProblemFromError(w, err)
			return
		}
		if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		// Resolve contact objects from stored ids (no plaintext on the row).
		out := map[string]any{}
		b, _ := json.Marshal(pf)
		_ = json.Unmarshal(b, &out)
		if len(pf.CVContactIDs) > 0 && d.Contacts != nil {
			if found, missing, rErr := d.Contacts.Resolve(r.Context(), pf.CVContactIDs); rErr == nil {
				if len(found) > 0 {
					out["platform_contacts"] = found
				}
				if len(missing) > 0 {
					out["missing_contact_ids"] = missing
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", etag)
		_ = json.NewEncoder(w).Encode(out)
	}
}

func putProfileFields(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
		if d.DB == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "unavailable", "database not configured")
			return
		}
		var body candidatestore.ProfileFields
		if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "invalid profile-fields body")
			return
		}
		// Transient contact_details → CreateContact + store IDs only.
		ensureCVBodyContacts(r.Context(), d, cand, &body)
		body.ContactDetails = nil
		if err := candidatestore.PutProfileFields(r.Context(), d.DB, cand, &body); err != nil {
			if errors.Is(err, candidatestore.ErrProfileNotFound) {
				httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", "profile not found")
				return
			}
			ProblemFromError(w, err)
			return
		}
		pf, etag, err := candidatestore.GetProfileFields(r.Context(), d.DB, cand)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", etag)
		out := map[string]any{"ok": true}
		b, _ := json.Marshal(pf)
		_ = json.Unmarshal(b, &out)
		_ = json.NewEncoder(w).Encode(out)
	}
}

func ensureCVBodyContacts(
	ctx context.Context,
	d *Deps,
	candidateID string,
	body *candidatestore.ProfileFields,
) {
	if d == nil || d.Contacts == nil || body == nil {
		return
	}
	details := profilecontacts.CollectDetails(body.ContactDetails)
	known, _ := candidatestore.GetCVContactIDs(ctx, d.DB, candidateID)
	if len(details) == 0 && len(known) == 0 {
		return
	}
	refs, _ := d.Contacts.EnsureDetails(ctx, details, known)
	if ids := profilecontacts.IDs(refs); len(ids) > 0 && d.DB != nil {
		_ = candidatestore.PutCVContactIDs(ctx, d.DB, candidateID, ids)
	}
}
