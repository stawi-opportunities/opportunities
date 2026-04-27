package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// CandidateStore is the read-only interface the match handler needs.
// Real impl is *candidatestore.Reader from pkg/candidatestore.
type CandidateStore interface {
	LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error)
	LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error)
}

// SearchRequest carries the parameters the match handler wants
// Manticore to filter on. Vector is the KNN query vector.
type SearchRequest struct {
	Vector             []float32
	Limit              int
	RemotePreference   string
	SalaryMinFloor     int
	PreferredLocations []string
}

// SearchHit is one Manticore row returned to the match handler.
//
// Reasons is an optional list of short, human-readable strings explaining
// why this opportunity matched (populated when the per-kind matcher is
// run; empty for the legacy CV-vs-job KNN-only pipeline).
type SearchHit struct {
	CanonicalID string
	Slug        string
	Title       string
	Company     string
	Score       float64
	Reasons     []string
}

// SearchIndex is the interface the handler depends on. Real impl
// wraps pkg/searchindex.Client.
type SearchIndex interface {
	KNNWithFilters(ctx context.Context, req SearchRequest) ([]SearchHit, error)
}

// MatchDeps bundles collaborators.
type MatchDeps struct {
	Svc    *frame.Service
	Store  CandidateStore
	Search SearchIndex
	TopK   int // default 20
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
	Company     string   `json:"company,omitempty"`
	Score       float64  `json:"score"`
	Reasons     []string `json:"reasons,omitempty"`
}

// MatchHandler returns an http.HandlerFunc for:
//
//	GET /candidates/match?candidate_id=cnd_X
//
// Reads the candidate's latest embedding + preferences from R2, queries
// Manticore for up to 200 KNN+filter candidates, takes the top-K by
// score, emits MatchesReadyV1, returns JSON.
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

		rows := make([]matchResponseRow, 0, len(res.Matches))
		eventRows := make([]eventsv1.MatchRow, 0, len(res.Matches))
		for _, h := range res.Matches {
			rows = append(rows, matchResponseRow(h))
			eventRows = append(eventRows, eventsv1.MatchRow{CanonicalID: h.CanonicalID, Score: h.Score})
		}
		resp := matchResponse{
			OK: true, CandidateID: res.CandidateID, MatchBatchID: res.MatchBatchID, Matches: rows,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)

		// Emit MatchesReadyV1 out-of-band so a slow event bus doesn't
		// add latency to the HTTP response, and a client disconnect
		// doesn't cancel the emit.
		env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
			CandidateID: res.CandidateID, MatchBatchID: res.MatchBatchID, Matches: eventRows,
		})
		go func() {
			emitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := deps.Svc.EventsManager().Emit(emitCtx, eventsv1.TopicCandidateMatchesReady, env); err != nil {
				util.Log(emitCtx).WithError(err).Warn("match: emit MatchesReadyV1 failed")
			}
		}()
	}
}
