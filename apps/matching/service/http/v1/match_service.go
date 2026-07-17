package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rs/xid"

	jobm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/job"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// ErrNoEmbedding is returned by MatchService.RunMatch when the
// candidate has no embedding yet (typically because they haven't
// uploaded a CV). HTTP callers translate this to 404; cron callers
// count it as "skipped" rather than "failed".
var ErrNoEmbedding = errors.New("match: no embedding for candidate")

// MatchResult is the structured output of RunMatch. Same shape the
// HTTP handler marshals into JSON.
type MatchResult struct {
	CandidateID  string
	MatchBatchID string
	Matches      []SearchHit
}

// MatchService runs the "embedding + preferences + PostgreSQL KNN +
// top-K truncation" pipeline for one candidate. Used by both the
// HTTP match handler and preference rematch. Scores are blended onto the
// same scale as FanOut/GapFill via matching.BlendFromCosine.
type MatchService struct {
	store    CandidateStore
	search   SearchIndex
	topK     int
	minScore float64
	weights  matching.Weights
}

// NewMatchService wires the service.
func NewMatchService(store CandidateStore, search SearchIndex, topK int) *MatchService {
	if topK <= 0 {
		topK = 20
	}
	return &MatchService{
		store: store, search: search, topK: topK,
		minScore: 0.45, weights: matching.DefaultWeights(),
	}
}

// WithMinScore sets the quality floor (default 0.45).
func (s *MatchService) WithMinScore(min float64) *MatchService {
	if min > 0 && min <= 1 {
		s.minScore = min
	}
	return s
}

// RunMatch loads the candidate's embedding + preferences, queries
// PostgreSQL, and returns the top-K hits. Does NOT emit events —
// that's the caller's job (HTTP handler emits after writing the
// response; cron emits in the weekly-digest loop).
func (s *MatchService) RunMatch(ctx context.Context, candidateID string) (MatchResult, error) {
	emb, err := s.store.LatestEmbedding(ctx, candidateID)
	if errors.Is(err, candidatestore.ErrNotFound) {
		return MatchResult{}, ErrNoEmbedding
	}
	if err != nil {
		return MatchResult{}, fmt.Errorf("match: load embedding: %w", err)
	}
	prefs, _ := s.store.LatestPreferences(ctx, candidateID)

	// MatchService handles the CV-vs-job pipeline; it cares only
	// about the "job" opt-in. Per-kind matching for other kinds runs
	// through the matcher registry (Phase 7.2-7.5) which reads the
	// same OptIns map directly.
	req := SearchRequest{Vector: emb.Vector, Limit: 200}
	if raw, ok := prefs.OptIns["job"]; ok && len(raw) > 0 {
		var jp jobm.JobPreferences
		if err := json.Unmarshal(raw, &jp); err != nil {
			return MatchResult{}, fmt.Errorf("match: decode job prefs: %w", err)
		}
		req.SalaryMinFloor = int(jp.SalaryMin)
		req.PreferredLocations = jp.Locations.Cities
		if jp.Locations.RemoteOK {
			req.RemotePreference = "remote"
		}
	}
	hits, err := s.search.KNNWithFilters(ctx, req)
	if err != nil {
		return MatchResult{}, err
	}
	// Re-score onto FanOut/GapFill scale: KNN returns cosine-like similarity;
	// BlendFromCosine applies the same weight structure as Score().
	minScore := s.minScore
	if minScore <= 0 {
		minScore = 0.45
	}
	w := s.weights
	if w == (matching.Weights{}) {
		w = matching.DefaultWeights()
	}
	filtered := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		// pgsearch returns 1 - distance. Recover distance for CosineFromPGDistance
		// so totals match FanOut/GapFill (which use CosineFromPGDistance).
		cos := matching.CosineFromPGDistance(1.0 - h.Score)
		// If KNN already returned a pure similarity in [0,1] with no transform quirks, prefer it when closer.
		if h.Score >= 0 && h.Score <= 1 {
			// Average of both interpretations is wrong — use CosineFromPGDistance
			// of recovered distance: distance = 1 - score when score = 1 - distance.
			cos = matching.CosineFromPGDistance(1.0 - h.Score)
		}
		total := matching.BlendFromCosine(cos, w)
		if total < minScore {
			continue
		}
		h.Score = total
		filtered = append(filtered, h)
	}
	hits = filtered
	if len(hits) > s.topK {
		hits = hits[:s.topK]
	}

	return MatchResult{
		CandidateID:  candidateID,
		MatchBatchID: xid.New().String(),
		Matches:      hits,
	}, nil
}

// Ensure the package uses the eventsv1 import beyond the type aliases
// already in match.go (keeps goimports happy in tests that reference
// shared fakes).
var _ = eventsv1.MatchesReadyV1{}
