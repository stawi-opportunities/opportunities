package billing

import (
	"context"
	"time"
)

// CandidateEntitlementWriter is the subset of CandidateRepository used for
// lifecycle entitlement mirroring.
type CandidateEntitlementWriter interface {
	ActivateSubscription(ctx context.Context, candidateID, subID, planID string) (bool, error)
	ScheduleCancelAtPeriodEnd(ctx context.Context, candidateID string) (time.Time, error)
	MarkPastDue(ctx context.Context, candidateID, subID string) error
	ExtendPaidPeriod(ctx context.Context, candidateID, subID, planID string, periodEnd *time.Time) error
	// HardCancel is optional; when nil, cancelled uses ScheduleCancelAtPeriodEnd only.
	// For immediate cancel after period end finalization, implement via FinalizeExpiredCancellations.
}

// CandidateMirror adapts CandidateRepository to EntitlementMirror.
type CandidateMirror struct {
	Repo CandidateEntitlementWriter
}

// ApplyPaid activates or extends paid entitlement after activated/billed events.
func (m *CandidateMirror) ApplyPaid(ctx context.Context, entityID, subscriptionID, planID string, periodEnd *time.Time) error {
	if m == nil || m.Repo == nil {
		return nil
	}
	if periodEnd != nil {
		return m.Repo.ExtendPaidPeriod(ctx, entityID, subscriptionID, planID, periodEnd)
	}
	// First activation path (also extends period_end +1m).
	_, err := m.Repo.ActivateSubscription(ctx, entityID, subscriptionID, planID)
	return err
}

// ApplyCancelled soft-cancels when atPeriodEnd, otherwise schedules cancel.
func (m *CandidateMirror) ApplyCancelled(ctx context.Context, entityID string, atPeriodEnd bool, _ *time.Time) error {
	if m == nil || m.Repo == nil {
		return nil
	}
	// Product always soft-cancels on the cancelled lifecycle event from billing
	// soft-cancel path; period-end hard cancel is FinalizeExpiredCancellations.
	_, err := m.Repo.ScheduleCancelAtPeriodEnd(ctx, entityID)
	_ = atPeriodEnd
	return err
}

// ApplyPastDue marks the candidate past_due for dunning UX.
func (m *CandidateMirror) ApplyPastDue(ctx context.Context, entityID, subscriptionID string) error {
	if m == nil || m.Repo == nil {
		return nil
	}
	return m.Repo.MarkPastDue(ctx, entityID, subscriptionID)
}
