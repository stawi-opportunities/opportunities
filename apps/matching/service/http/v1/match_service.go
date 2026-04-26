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

// MatchService runs the "embedding + preferences + Manticore KNN +
// top-K truncation" pipeline for one candidate. Used by both the
// HTTP match handler and the weekly-digest cron.
type MatchService struct {
	store  CandidateStore
	search SearchIndex
	topK   int
}

// NewMatchService wires the service.
func NewMatchService(store CandidateStore, search SearchIndex, topK int) *MatchService {
	if topK <= 0 {
		topK = 20
	}
	return &MatchService{store: store, search: search, topK: topK}
}

// RunMatch loads the candidate's embedding + preferences, queries
// Manticore, and returns the top-K hits. Does NOT emit events —
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

	// MatchService is the legacy CV-vs-job pipeline; it cares only
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
