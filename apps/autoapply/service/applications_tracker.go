package service

import (
	"context"
	"errors"

	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

// PkgApplicationsTracker bridges the autoapply handler to main's
// pkg/applications.Store. It implements ApplicationsTracker by mapping
// every successful submission into a single applications row with
// Status=StatusSubmitted.
//
// We deliberately skip the StatusApplying intermediate: by the time
// the autoapply pipeline writes a tracking row the submission has
// already completed. The "applying" state exists in main's machine
// for candidates who started the apply manually and may take time to
// confirm — that's a different ingress path.
//
// Idempotency: pkg/applications.Store.Create returns ErrAlreadyExists
// on (candidate_id, opportunity_id) conflict. We treat that as success
// — the candidate already has a row for this job (perhaps from a
// prior manual attempt or a retry of this same intent) and the
// existing record is the source of truth.
type PkgApplicationsTracker struct {
	store *applications.Store
}

// NewPkgApplicationsTracker returns a tracker backed by the supplied
// store. Passing a nil store returns nil so callers can wire
// HandlerDeps.Tracker = NewPkgApplicationsTracker(nil) unconditionally
// when the store is optional.
func NewPkgApplicationsTracker(store *applications.Store) *PkgApplicationsTracker {
	if store == nil {
		return nil
	}
	return &PkgApplicationsTracker{store: store}
}

// Record implements ApplicationsTracker.
func (t *PkgApplicationsTracker) Record(ctx context.Context, in TrackerRecord) error {
	if t == nil || t.store == nil {
		return nil
	}
	if in.CandidateID == "" || in.CanonicalJobID == "" {
		return errors.New("applications tracker: candidate_id and canonical_job_id are required")
	}
	app := applications.Application{
		ApplicationID: xid.New().String(),
		CandidateID:   in.CandidateID,
		OpportunityID: in.CanonicalJobID,
		MatchID:       in.MatchID,
		Status:        applications.StatusSubmitted,
		Metadata: map[string]any{
			"source":       "autoapply",
			"method":       in.Method,
			"source_type":  in.SourceType,
			"external_ref": in.ExternalRef,
		},
	}
	_, err := t.store.Create(ctx, app)
	if errors.Is(err, applications.ErrAlreadyExists) {
		// Row already exists for this (candidate, opportunity) pair —
		// either a redelivery of this intent or a prior manual record.
		// Either way, the existing row wins; we don't ApplyTransition
		// because that would risk overwriting candidate-edited state.
		return nil
	}
	return err
}
