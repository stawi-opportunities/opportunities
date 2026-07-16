package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// CandidateRepository wraps GORM operations for candidate profiles.
type CandidateRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewCandidateRepository creates a new CandidateRepository.
func NewCandidateRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *CandidateRepository {
	return &CandidateRepository{db: db}
}

// Create inserts a new candidate profile.
func (r *CandidateRepository) Create(ctx context.Context, c *domain.CandidateProfile) error {
	return r.db(ctx, false).Create(c).Error
}

// GetByID retrieves a candidate profile by primary key.
// Returns nil, nil if no record is found.
func (r *CandidateRepository) GetByID(ctx context.Context, id string) (*domain.CandidateProfile, error) {
	var c domain.CandidateProfile
	err := r.db(ctx, true).Where("id = ?", id).First(&c).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// GetByProfileID retrieves a candidate profile by external profile ID (JWT sub claim).
// Returns nil, nil if no record is found.
func (r *CandidateRepository) GetByProfileID(ctx context.Context, profileID string) (*domain.CandidateProfile, error) {
	var c domain.CandidateProfile
	err := r.db(ctx, true).Where("profile_id = ?", profileID).First(&c).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// Update saves all fields of the given candidate profile.
func (r *CandidateRepository) Update(ctx context.Context, c *domain.CandidateProfile) error {
	return r.db(ctx, false).Save(c).Error
}

// UpdateStatus changes only the status field for the given candidate ID.
func (r *CandidateRepository) UpdateStatus(ctx context.Context, id string, status domain.CandidateStatus) error {
	return r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// IncrementMatchesSent increments the matches_sent counter and updates last_contacted_at.
func (r *CandidateRepository) IncrementMatchesSent(ctx context.Context, id string) error {
	now := time.Now()
	return r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"matches_sent":      gorm.Expr("matches_sent + 1"),
			"last_contacted_at": now,
		}).Error
}

// ListActive returns active candidate profiles up to the given limit.
func (r *CandidateRepository) ListActive(ctx context.Context, limit int) ([]*domain.CandidateProfile, error) {
	var candidates []*domain.CandidateProfile
	err := r.db(ctx, true).
		Where("status = ?", domain.CandidateActive).
		Order("id ASC").
		Limit(limit).
		Find(&candidates).Error
	return candidates, err
}

// ListPaidActive returns ACTIVE candidates entitled to match digests:
// paid, past_due (dunning grace), and trial. Free/cancelled are excluded —
// they use the unpaid weekly jobs digest instead.
func (r *CandidateRepository) ListPaidActive(ctx context.Context, limit int) ([]*domain.CandidateProfile, error) {
	if limit <= 0 {
		limit = 5000
	}
	var candidates []*domain.CandidateProfile
	err := r.db(ctx, true).
		Where("status = ?", domain.CandidateActive).
		Where("subscription IN ?", []domain.SubscriptionTier{
			domain.SubscriptionPaid,
			domain.SubscriptionPastDue,
			domain.SubscriptionTrial,
		}).
		Order("id ASC").
		Limit(limit).
		Find(&candidates).Error
	return candidates, err
}

// UpdateNotificationPrefs persists digest/notification settings for a candidate.
func (r *CandidateRepository) UpdateNotificationPrefs(ctx context.Context, candidateID, emailDigest string, matchAlerts, weeklySummary, marketingEmails bool) error {
	return r.db(ctx, false).Model(&domain.CandidateProfile{}).
		Where("id = ?", candidateID).
		Updates(map[string]any{
			"email_digest":     emailDigest,
			"match_alerts":     matchAlerts,
			"weekly_summary":   weeklySummary,
			"marketing_emails": marketingEmails,
		}).Error
}

// TouchLastDigestAt records when a summary digest was successfully emitted.
func (r *CandidateRepository) TouchLastDigestAt(ctx context.Context, candidateID string, at time.Time) error {
	return r.db(ctx, false).Model(&domain.CandidateProfile{}).
		Where("id = ?", candidateID).
		Update("last_digest_at", at).Error
}

// ListAll returns all candidate profiles with pagination.
func (r *CandidateRepository) ListAll(ctx context.Context, limit, offset int) ([]*domain.CandidateProfile, error) {
	var candidates []*domain.CandidateProfile
	err := r.db(ctx, true).
		Order("id ASC").
		Limit(limit).
		Offset(offset).
		Find(&candidates).Error
	return candidates, err
}

// Count returns the total number of candidate profile records.
func (r *CandidateRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.CandidateProfile{}).Count(&count).Error
	return count, err
}

// ActivateSubscription flips a candidate to the paid tier on a confirmed
// payment. Idempotent: it only writes when the row is not already
// (subscription=paid, subscription_id=subID) so a webhook + the
// reconciler can both fire for the same payment without double-effects.
// Returns whether the row was changed. planID, when non-empty, confirms
// the persisted plan; subID links the candidate to the billing
// subscription.
//
// AutoApply is set from plan entitlements (Pro/Managed true, Starter false)
// so higher tiers unlock automated apply without a separate flag write.
func (r *CandidateRepository) ActivateSubscription(ctx context.Context, candidateID, subID, planID string) (bool, error) {
	periodEnd := time.Now().UTC().AddDate(0, 1, 0) // monthly period
	updates := map[string]interface{}{
		"subscription":         domain.SubscriptionPaid,
		"subscription_id":      subID,
		"current_period_end":   periodEnd,
		"cancel_at_period_end": false,
	}
	if planID != "" {
		updates["plan_id"] = planID
	}
	// Entitlements: auto-apply for pro/managed; starter is matches-only.
	switch planID {
	case "pro", "managed":
		updates["auto_apply"] = true
	case "starter":
		updates["auto_apply"] = false
	}
	res := r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ? AND NOT (subscription = ? AND subscription_id = ? AND cancel_at_period_end = ?)",
			candidateID, domain.SubscriptionPaid, subID, false).
		Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ScheduleCancelAtPeriodEnd marks a paid subscription to end at current_period_end
// without immediate entitlement loss. Returns the effective end time.
func (r *CandidateRepository) ScheduleCancelAtPeriodEnd(ctx context.Context, candidateID string) (time.Time, error) {
	cand, err := r.GetByID(ctx, candidateID)
	if err != nil {
		return time.Time{}, err
	}
	if cand.Subscription != domain.SubscriptionPaid {
		return time.Time{}, fmt.Errorf("no active paid subscription")
	}
	end := time.Now().UTC().AddDate(0, 1, 0)
	if cand.CurrentPeriodEnd != nil && cand.CurrentPeriodEnd.After(time.Now().UTC()) {
		end = cand.CurrentPeriodEnd.UTC()
	}
	res := r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", candidateID).
		Updates(map[string]interface{}{
			"cancel_at_period_end": true,
			"current_period_end":   end,
		})
	if res.Error != nil {
		return time.Time{}, res.Error
	}
	return end, nil
}

// ChangePlan updates plan_id + entitlements for an active subscriber.
// Downgrades take effect at period end (pending_plan stored via plan_id now for
// simplicity after end; upgrades apply immediately).
func (r *CandidateRepository) ChangePlan(ctx context.Context, candidateID, newPlanID string, immediate bool) error {
	updates := map[string]interface{}{
		"plan_id": newPlanID,
	}
	if immediate {
		updates["cancel_at_period_end"] = false
		switch newPlanID {
		case "pro", "managed":
			updates["auto_apply"] = true
		case "starter":
			updates["auto_apply"] = false
		}
	}
	res := r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ? AND subscription = ?", candidateID, domain.SubscriptionPaid).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("no active paid subscription")
	}
	return nil
}

// FinalizeExpiredCancellations flips paid→cancelled when cancel_at_period_end
// and current_period_end are past. Safe for cron/reconcile.
func (r *CandidateRepository) FinalizeExpiredCancellations(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	res := r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("subscription = ? AND cancel_at_period_end = ? AND current_period_end IS NOT NULL AND current_period_end < ?",
			domain.SubscriptionPaid, true, time.Now().UTC()).
		Limit(limit).
		Updates(map[string]interface{}{
			"subscription":         domain.SubscriptionCancelled,
			"cancel_at_period_end": false,
			"auto_apply":           false,
		})
	return res.RowsAffected, res.Error
}

// MarkPastDue sets subscription=past_due for dunning UX after rebill failures.
// Idempotent for an already past_due row with the same subscription_id.
func (r *CandidateRepository) MarkPastDue(ctx context.Context, candidateID, subID string) error {
	updates := map[string]interface{}{
		"subscription": domain.SubscriptionPastDue,
	}
	if subID != "" {
		updates["subscription_id"] = subID
	}
	res := r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", candidateID).
		Updates(updates)
	return res.Error
}

// ExtendPaidPeriod refreshes current_period_end after a successful rebill
// (lifecycle subscription.billed). Keeps subscription=paid.
func (r *CandidateRepository) ExtendPaidPeriod(ctx context.Context, candidateID, subID, planID string, periodEnd *time.Time) error {
	updates := map[string]interface{}{
		"subscription":         domain.SubscriptionPaid,
		"cancel_at_period_end": false,
	}
	if subID != "" {
		updates["subscription_id"] = subID
	}
	if planID != "" {
		updates["plan_id"] = planID
	}
	if periodEnd != nil {
		updates["current_period_end"] = periodEnd.UTC()
	} else {
		updates["current_period_end"] = time.Now().UTC().AddDate(0, 1, 0)
	}
	res := r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", candidateID).
		Updates(updates)
	return res.Error
}

// ListPendingSubscriptions returns candidates with a SubscriptionID
// that are not yet marked as paid. The billing reconciler uses this
// to poll service_billing for state transitions so the candidate row
// reflects the authoritative lifecycle state even when webhooks drop.
func (r *CandidateRepository) ListPendingSubscriptions(ctx context.Context, limit int) ([]*domain.CandidateProfile, error) {
	var candidates []*domain.CandidateProfile
	err := r.db(ctx, true).
		Where("subscription_id <> '' AND subscription <> ?", domain.SubscriptionPaid).
		Order("id ASC").
		Limit(limit).
		Find(&candidates).Error
	return candidates, err
}

// ListUnpaidWithProfile returns candidates who completed signup +
// onboarding but have not finished checkout — i.e. their effective
// subscription is not 'paid'. Used by the weekly jobs digest cron to
// re-engage these users with a non-personalised "new jobs this week"
// email.
//
// "Unpaid" is defined as one of:
//   - Subscription IN (free, trial, cancelled)
//   - Subscription = paid BUT SubscriptionID = ” (the candidate row
//     was set to 'paid' optimistically without a backing subscription;
//     the reconciler will eventually correct this)
//
// We exclude rows where Status='unverified' since those candidates
// haven't gone through onboarding yet and don't have country / opt-in
// signal to personalise on. Result is ordered by id so the sweep is
// deterministic across runs.
func (r *CandidateRepository) ListUnpaidWithProfile(ctx context.Context, limit int) ([]*domain.CandidateProfile, error) {
	var out []*domain.CandidateProfile
	err := r.db(ctx, true).
		Where("status <> ? AND (subscription IN ? OR subscription_id = '')",
			domain.CandidateUnverified,
			[]domain.SubscriptionTier{
				domain.SubscriptionFree,
				domain.SubscriptionTrial,
				domain.SubscriptionCancelled,
			}).
		Order("id ASC").
		Limit(limit).
		Find(&out).Error
	return out, err
}

// ListInactiveSince returns active candidates whose `updated_at` is
// older than cutoff, up to limit rows. Used by the daily stale-nudge
// cron as a proxy for "no recent activity".
func (r *CandidateRepository) ListInactiveSince(ctx context.Context, cutoff time.Time, limit int) ([]*domain.CandidateProfile, error) {
	var out []*domain.CandidateProfile
	err := r.db(ctx, true).
		Where("status = ? AND updated_at < ?", domain.CandidateActive, cutoff).
		Order("updated_at ASC").
		Limit(limit).
		Find(&out).Error
	return out, err
}

// GetOnboardingDraft returns the candidate's persisted wizard draft as
// raw JSON. Unknown candidates return `{}` rather than an error — the
// onboarding handler treats "no draft" identically to "candidate
// doesn't exist yet"; a fresh user has the same empty wizard either
// way.
func (r *CandidateRepository) GetOnboardingDraft(ctx context.Context, id string) ([]byte, error) {
	var result struct {
		OnboardingDraft []byte `gorm:"column:onboarding_draft"`
	}
	err := r.db(ctx, true).
		Raw(`SELECT onboarding_draft FROM candidate_profiles WHERE id = ? LIMIT 1`, id).
		Scan(&result).Error
	if err != nil {
		return nil, err
	}
	if len(result.OnboardingDraft) == 0 {
		return []byte("{}"), nil
	}
	return result.OnboardingDraft, nil
}

// SetOnboardingDraft writes the wizard draft. The body is opaque to
// the repository; the matching service owns the schema. The candidate
// row must already exist — sign-in creates one before the wizard
// mounts (see [POST /candidates/onboard] for the canonical row
// creation path; a draft-only candidate is created lazily by
// SetOnboardingDraft via an UPDATE that hits zero rows then INSERTs
// the bare minimum).
func (r *CandidateRepository) SetOnboardingDraft(ctx context.Context, id string, draft []byte) error {
	res := r.db(ctx, false).
		Exec(`UPDATE candidate_profiles SET onboarding_draft = ?::jsonb WHERE id = ?`, string(draft), id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Lazy-create the candidate row so the wizard can persist
		// before the final /candidates/onboard call. The row uses
		// id as its own profile_id (they're the same value pre-Phase-4
		// split, set by the auth layer). All other columns get their
		// NOT NULL DEFAULT values from the schema.
		return r.db(ctx, false).Exec(
			`INSERT INTO candidate_profiles (id, onboarding_draft) VALUES (?, ?::jsonb)
			 ON CONFLICT (id) DO UPDATE SET onboarding_draft = EXCLUDED.onboarding_draft`,
			id, string(draft),
		).Error
	}
	return nil
}

// ClearOnboardingDraft resets the draft to '{}'. Called inside the
// same transaction as POST /candidates/onboard so a successful submit
// atomically promotes the draft into the canonical profile columns.
func (r *CandidateRepository) ClearOnboardingDraft(ctx context.Context, id string) error {
	return r.db(ctx, false).
		Exec(`UPDATE candidate_profiles SET onboarding_draft = '{}'::jsonb WHERE id = ?`, id).Error
}

// Transaction runs fn inside a single database transaction. The
// closure receives a CandidateRepository scoped to the transaction —
// any write methods called on it (Update, ClearOnboardingDraft, etc.)
// commit or rollback atomically with each other. Used by the
// /candidates/onboard handler to promote the wizard draft into the
// canonical profile columns and clear the draft in one atomic step.
func (r *CandidateRepository) Transaction(ctx context.Context, fn func(txRepo *CandidateRepository) error) error {
	return r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		txRepo := &CandidateRepository{db: func(_ context.Context, _ bool) *gorm.DB { return tx }}
		return fn(txRepo)
	})
}
