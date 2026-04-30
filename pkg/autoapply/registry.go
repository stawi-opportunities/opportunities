package autoapply

import (
	"context"

	"github.com/pitabwire/util"
)

// Registry is the ordered list of Submitters. The first submitter whose
// CanHandle returns true wins. If none match, Submit returns a
// "skipped/no_submitter" result with a nil error.
type Registry struct {
	tiers []Submitter
}

// NewRegistry constructs a Registry from an ordered list of Submitters.
// Callers register tiers from most-specific (ATS UI) to least-specific
// (email fallback).
func NewRegistry(tiers ...Submitter) *Registry {
	return &Registry{tiers: tiers}
}

// Submit routes the request to the first matching submitter. Returns a
// no-error skip result when no submitter claims the URL.
func (r *Registry) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
	log := util.Log(ctx).
		WithField("candidate_id", req.CandidateID).
		WithField("apply_url", req.ApplyURL)

	for _, s := range r.tiers {
		if !s.CanHandle(req.SourceType, req.ApplyURL) {
			continue
		}
		log.WithField("submitter", s.Name()).Debug("autoapply: routing to submitter")
		res, err := s.Submit(ctx, req)
		if err != nil {
			return SubmitResult{}, err
		}
		return res, nil
	}

	log.Debug("autoapply: no submitter matched; skipping")
	return SubmitResult{Method: "skipped", SkipReason: "no_submitter"}, nil
}
