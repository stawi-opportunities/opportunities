package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

// CandidateStore is the read-only interface the match handler needs.
// Real impl is *candidatestore.Reader from pkg/candidatestore.
type CandidateStore interface {
	LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error)
	LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error)
}

// SearchRequest carries the parameters the match handler wants the
// search adapter to filter on. Vector is the KNN query vector.
type SearchRequest struct {
	Vector             []float32
	Limit              int
	RemotePreference   string
	SalaryMinFloor     int
	PreferredLocations []string
}

// SearchHit is one row from the opportunities canonical store returned
// to the match handler.
//
// Reasons is an optional list of short, human-readable strings explaining
// why this opportunity matched (populated when the per-kind matcher is
// run; empty for the CV-vs-job KNN-only pipeline).
type SearchHit struct {
	CanonicalID string
	Slug        string
	Title       string
	ApplyURL    string
	Company     string
	Score       float64
	Reasons     []string
}

// SearchIndex is the interface the handler depends on. The production
// implementation is *PostgresSearch (pgvector KNN + JSONB filters).
type SearchIndex interface {
	KNNWithFilters(ctx context.Context, req SearchRequest) ([]SearchHit, error)
}

// MatchDeps bundles collaborators.
type MatchDeps struct {
	Svc     *frame.Service
	Store   CandidateStore
	Search  SearchIndex
	Persist MatchPersister // optional; when set, results land in candidate_matches
	TopK    int            // default 20
	// RequireAuthCandidate when non-empty forces the query candidate_id to
	// match the authenticated identity (prevents IDOR on the legacy route).
	RequireAuthCandidate string
	// WantsAlerts sets delivery=immediate on notification service payloads.
	WantsAlerts func(ctx context.Context, candidateID string) bool
	// Notifier delivers via service-notification (required path for user mail).
	Notifier *notify.Notifier
}

// matchResponse is the JSON body returned from the handler.
type matchResponse struct {
	OK           bool               `json:"ok"`
	CandidateID  string             `json:"candidate_id"`
	MatchBatchID string             `json:"match_batch_id"`
	Matches      []matchResponseRow `json:"matches"`
}

type matchResponseRow struct {
	CanonicalID string   `json:"canonical_id"`
	Slug        string   `json:"slug,omitempty"`
	Title       string   `json:"title,omitempty"`
	ApplyURL    string   `json:"apply_url"`
	Company     string   `json:"company,omitempty"`
	Score       float64  `json:"score"`
	Reasons     []string `json:"reasons,omitempty"`
}

// MatchHandler returns an http.HandlerFunc for:
//
//	GET /candidates/match?candidate_id=cnd_X
//
// Prefer wrapping with CandidateAuth and omitting candidate_id so the
// JWT subject is used. When candidate_id is supplied it must match the
// authenticated subject (when RequireAuthCandidate is set via auth MW
// context, or when both are present).
//
// Reads the candidate's latest embedding + preferences, runs
// a pgvector KNN-with-filters query, takes the top-K by score, persists
// into candidate_matches, emits MatchesReadyV1, returns JSON.
func MatchHandler(deps MatchDeps) http.HandlerFunc {
	svcPipeline := NewMatchService(deps.Store, deps.Search, deps.TopK)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		candidateID := strings.TrimSpace(r.URL.Query().Get("candidate_id"))
		// Prefer authenticated identity when available.
		if authID, ok := candidateIDFromAuth(ctx); ok {
			if candidateID != "" && candidateID != authID {
				http.Error(w, `{"error":"candidate_id does not match authenticated subject"}`, http.StatusForbidden)
				return
			}
			candidateID = authID
		}
		if candidateID == "" {
			http.Error(w, `{"error":"candidate_id is required"}`, http.StatusBadRequest)
			return
		}

		res, err := svcPipeline.RunMatch(ctx, candidateID)
		if errors.Is(err, ErrNoEmbedding) {
			http.Error(w, `{"error":"embedding not available — upload CV first"}`, http.StatusNotFound)
			return
		}
		if err != nil {
			log.WithError(err).Error("match: RunMatch failed")
			http.Error(w, `{"error":"search failed"}`, http.StatusBadGateway)
			return
		}

		// Always collect matches for the dashboard / API response.
		if err := PersistMatchResult(ctx, deps.Persist, res, "http_match"); err != nil {
			log.WithError(err).Warn("match: persist candidate_matches failed")
		}

		rows := make([]matchResponseRow, 0, len(res.Matches))
		eventRows := make([]eventsv1.MatchRow, 0, len(res.Matches))
		for _, h := range res.Matches {
			rows = append(rows, matchResponseRow(h))
			eventRows = append(eventRows, eventsv1.MatchRow{CanonicalID: h.CanonicalID, ApplyURL: h.ApplyURL, Score: h.Score})
		}
		resp := matchResponse{
			OK: true, CandidateID: res.CandidateID, MatchBatchID: res.MatchBatchID, Matches: rows,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)

		immediate := deps.WantsAlerts != nil && deps.WantsAlerts(ctx, res.CandidateID)
		items := make([]notify.MatchItem, 0, len(res.Matches))
		for _, h := range res.Matches {
			items = append(items, notify.MatchItem{
				CanonicalID: h.CanonicalID, Title: h.Title, Company: h.Company,
				ApplyURL: h.ApplyURL, Slug: h.Slug, Score: h.Score,
			})
		}
		go func() {
			emitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Always queue via service-notification.
			if deps.Notifier != nil && len(items) > 0 {
				deps.Notifier.MatchesReady(emitCtx, res.CandidateID, res.MatchBatchID, items, immediate)
			}
			if deps.Svc != nil {
				env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
					CandidateID: res.CandidateID, MatchBatchID: res.MatchBatchID, Matches: eventRows,
				})
				if err := deps.Svc.EventsManager().Emit(emitCtx, eventsv1.TopicCandidateMatchesReady, env); err != nil {
					util.Log(emitCtx).WithError(err).Debug("match: domain event emit failed")
				}
			}
		}()
	}
}

// candidateIDFromAuth returns the candidate id if CandidateAuth has run.
func candidateIDFromAuth(ctx context.Context) (string, bool) {
	return httpmw.CandidateFromContextOptional(ctx)
}
