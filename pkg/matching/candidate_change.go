package matching

import (
	"context"
	"errors"
	"time"
)

// Debouncer guards Path C against thrashing when a candidate flips
// rules / embedding rapidly. Typical impl: a Valkey SETNX with TTL.
type Debouncer interface {
	// Acquire returns true if the caller may proceed. False means the
	// candidate has already been rematched within the debounce window
	// and the caller should drop the request.
	Acquire(ctx context.Context, candidateID string, ttl time.Duration) (bool, error)
}

// ErrDebounced is returned when the debounce lock denies the rematch.
var ErrDebounced = errors.New("matching: rematch debounced")

// CandidateChangeDeps composes the dependencies.
type CandidateChangeDeps struct {
	Debouncer   Debouncer
	DebounceTTL time.Duration
	GapFill     GapFillDeps
}

// CandidateChange is the trigger payload.
type CandidateChange struct {
	CandidateID    string
	Embedding      []float32
	Skills         []string
	Countries      []string
	Kinds          []string
	SalaryFloorUSD *int
	MinScore       float64
	TriggeredBy    string // rules_changed | cv_changed | admin
	// QueryText is the candidate-side text (CV summary / skills) fed to the
	// cross-encoder as the rerank query. Empty disables reranking for this run.
	QueryText string
}

// RunCandidateChange executes Path C. Returns ErrDebounced if the
// debounce lock rejects the request.
func RunCandidateChange(ctx context.Context, in CandidateChange, deps CandidateChangeDeps) (GapFillResult, error) {
	ttl := deps.DebounceTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	ok, err := deps.Debouncer.Acquire(ctx, in.CandidateID, ttl)
	if err != nil {
		return GapFillResult{}, err
	}
	if !ok {
		return GapFillResult{}, ErrDebounced
	}

	since := time.Now().Add(-30 * 24 * time.Hour)

	return GapFill(ctx, GapFillInput{
		CandidateID:    in.CandidateID,
		Embedding:      in.Embedding,
		Skills:         in.Skills,
		Countries:      in.Countries,
		Kinds:          in.Kinds,
		SalaryFloorUSD: in.SalaryFloorUSD,
		Since:          since,
		MinScore:       in.MinScore,
		QueryText:      in.QueryText,
	}, deps.GapFill)
}
