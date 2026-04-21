package v1

import (
	"context"
	"errors"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	httpv1 "stawi.jobs/apps/candidates/service/http/v1"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// ServiceMatchRunner wraps an *httpv1.MatchService so the cron calls
// the same pipeline the HTTP handler uses. Emits MatchesReadyV1 after
// a successful run; swallows ErrNoEmbedding (candidate without a CV is
// not a cron failure — we just skip them).
type ServiceMatchRunner struct {
	svc   *frame.Service
	match *httpv1.MatchService
}

// NewServiceMatchRunner wires the runner.
func NewServiceMatchRunner(svc *frame.Service, match *httpv1.MatchService) *ServiceMatchRunner {
	return &ServiceMatchRunner{svc: svc, match: match}
}

// RunMatch runs the match pipeline for one candidate and emits
// MatchesReadyV1. Returns nil for ErrNoEmbedding so the cron counts
// such candidates as "processed" rather than "failed".
func (r *ServiceMatchRunner) RunMatch(ctx context.Context, candidateID string) error {
	res, err := r.match.RunMatch(ctx, candidateID)
	if errors.Is(err, httpv1.ErrNoEmbedding) {
		util.Log(ctx).WithField("candidate_id", candidateID).Debug("match-runner: no embedding; skipping")
		return nil
	}
	if err != nil {
		return err
	}

	rows := make([]eventsv1.MatchRow, 0, len(res.Matches))
	for _, m := range res.Matches {
		rows = append(rows, eventsv1.MatchRow{CanonicalID: m.CanonicalID, Score: m.Score})
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
		CandidateID:  res.CandidateID,
		MatchBatchID: res.MatchBatchID,
		Matches:      rows,
	})
	if err := r.svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); err != nil {
		return err
	}
	return nil
}
