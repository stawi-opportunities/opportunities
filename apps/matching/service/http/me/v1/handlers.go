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

	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
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
	// DefaultMinScore floors on-demand gap-fill when the index has no
	// per-candidate threshold (MATCHING_MIN_SCORE). 0 → 0.45.
	DefaultMinScore float64

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
		cand := httpmw.CandidateFromContext(ctx)
		if d.DB == nil || d.IndexStore == nil || d.KNN == nil || d.Matches == nil {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "matching_unavailable", "match pipeline not configured")
			return
		}
		// Paid (or past_due still entitled) only — free users use public search.
		var sub string
		if err := d.DB.QueryRowContext(ctx,
			`SELECT COALESCE(subscription,'') FROM candidate_profiles WHERE id = $1`, cand,
		).Scan(&sub); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", "profile not found")
				return
			}
			ProblemFromError(w, err)
			return
		}
		switch strings.ToLower(strings.TrimSpace(sub)) {
		case "paid", "past_due", "trial":
			// ok
		default:
			httpmw.ProblemJSON(w, http.StatusPaymentRequired, "subscription_required",
				"active subscription required to refresh matches")
			return
		}

		idx, err := d.IndexStore.Get(ctx, cand)
		if err != nil || idx == nil || len(idx.Embedding) == 0 {
			httpmw.ProblemJSON(w, http.StatusConflict, "no_embedding",
				"upload a CV and wait for embedding before refreshing matches")
			return
		}
		minScore := effectiveMinScore(idx.MinScore, d.DefaultMinScore)
		since := time.Now().UTC().Add(-30 * 24 * time.Hour)
		res, runErr := matching.GapFill(ctx, matching.GapFillInput{
			CandidateID:    cand,
			Embedding:      idx.Embedding,
			Countries:      idx.Countries,
			Kinds:          idx.Kinds,
			SalaryFloorUSD: idx.SalaryFloorUSD,
			Since:          since,
			MinScore:       minScore,
			DailyCap:       idx.DailyCap,
			WeeklyCap:      idx.WeeklyCap,
		}, matching.GapFillDeps{
			KNN:      d.KNN,
			Store:    d.Matches,
			EventLog: d.MatchEvents,
			Reranker: d.Reranker,
			Weights:  d.Weights,
		})
		if runErr != nil {
			ProblemFromError(w, runErr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":              true,
			"matches_written": res.MatchesWritten,
			"opps_scanned":    res.OppsScanned,
			"run_id":          res.RunID,
			"min_score":       minScore,
		})
	}
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
       COALESCE(match_alerts, true),
       COALESCE(weekly_summary, true),
       COALESCE(marketing_emails, false),
       COALESCE(comm_email, true)
FROM candidate_profiles WHERE id = $1`, cand).Scan(&digest, &matchAl, &weekly, &market, &commMail)
		if errors.Is(err, sql.ErrNoRows) {
			// New account defaults — still return a valid prefs object.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(notificationPrefsResp{
				EmailDigest: matching.DigestWeekly, MatchAlerts: true, WeeklySummary: true,
			})
			return
		}
		if err != nil {
			// Columns may not exist yet on a lagging migrate — fall back to defaults.
			if strings.Contains(err.Error(), "email_digest") || strings.Contains(err.Error(), "does not exist") {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(notificationPrefsResp{
					EmailDigest: matching.DigestWeekly, MatchAlerts: true, WeeklySummary: true,
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

// ---- GET /api/me/profile-fields ----

func profileFields(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := httpmw.CandidateFromContext(r.Context())
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
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", etag)
		_ = json.NewEncoder(w).Encode(pf)
	}
}
