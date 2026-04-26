package v1

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/xid"

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

	hits, err := s.search.KNNWithFilters(ctx, SearchRequest{
		Vector:             emb.Vector,
		Limit:              200,
		RemotePreference:   prefs.RemotePreference,
		SalaryMinFloor:     prefs.SalaryMin,
		PreferredLocations: prefs.PreferredLocations,
	})
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
