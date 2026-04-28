// Package repository — opportunity-flag persistence.
//
// Patterned after SourceRepository: one struct, narrow methods, every
// call funnels through a *gorm.DB factory so the candidate-store and
// api processes can share the same datastore-pool wiring.
package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// FlagRepository wraps GORM operations for the OpportunityFlag entity.
type FlagRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewFlagRepository creates a new FlagRepository.
func NewFlagRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *FlagRepository {
	return &FlagRepository{db: db}
}

// Create inserts a new flag row. The unique-index on
// (opportunity_slug, submitted_by) WHERE deleted_at IS NULL surfaces a
// duplicate as a constraint violation — callers map that to 409.
func (r *FlagRepository) Create(ctx context.Context, f *domain.OpportunityFlag) error {
	return r.db(ctx, false).Create(f).Error
}

// GetByID returns a flag by its primary key. Returns (nil, nil) when
// the row does not exist, matching the wider repository convention.
func (r *FlagRepository) GetByID(ctx context.Context, id string) (*domain.OpportunityFlag, error) {
	var f domain.OpportunityFlag
	err := r.db(ctx, true).Where("id = ?", id).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ExistsForUser reports whether a non-soft-deleted flag from
// submittedBy on slug already exists. Used for fast-path 409 in the
// user-facing POST handler before attempting an insert.
func (r *FlagRepository) ExistsForUser(ctx context.Context, slug, submittedBy string) (bool, error) {
	var count int64
	err := r.db(ctx, true).
		Model(&domain.OpportunityFlag{}).
		Where("opportunity_slug = ? AND submitted_by = ?", slug, submittedBy).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListByOpportunity returns every (non-soft-deleted) flag for a slug,
// most-recent-first. Used by the operator review surface and by the
// ban_source action to find every flag pointing at the bad listing.
func (r *FlagRepository) ListByOpportunity(ctx context.Context, slug string) ([]*domain.OpportunityFlag, error) {
	var flags []*domain.OpportunityFlag
	err := r.db(ctx, true).
		Where("opportunity_slug = ?", slug).
		Order("created_at DESC").
		Find(&flags).Error
	return flags, err
}

// ListUnresolved returns the unresolved-flag review queue, paginated.
// Filters: reason (optional, exact match), slug (optional). Order is
// most-recent-first so freshly-flagged items surface for triage.
func (r *FlagRepository) ListUnresolved(ctx context.Context, reason domain.FlagReason, slug string, limit, offset int) ([]*domain.OpportunityFlag, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := r.db(ctx, true).
		Model(&domain.OpportunityFlag{}).
		Where("resolved_at IS NULL")
	if reason != "" {
		q = q.Where("reason = ?", reason)
	}
	if slug != "" {
		q = q.Where("opportunity_slug = ?", slug)
	}
	var flags []*domain.OpportunityFlag
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&flags).Error
	return flags, err
}

// CountUnresolvedByOpportunity counts distinct unresolved scam flags
// from distinct users on slug. Used by the threshold check after each
// new flag insertion.
//
// "Distinct submitter" is enforced by the unique index, so a plain
// COUNT(*) over (slug, reason='scam', resolved_at IS NULL) is correct
// — duplicates can never make it into the table in the first place.
func (r *FlagRepository) CountUnresolvedByOpportunity(ctx context.Context, slug string) (int, error) {
	var count int64
	err := r.db(ctx, true).
		Model(&domain.OpportunityFlag{}).
		Where("opportunity_slug = ? AND reason = ? AND resolved_at IS NULL",
			slug, domain.FlagScam).
		Count(&count).Error
	return int(count), err
}

// Resolve stamps resolved_at / resolved_by / resolution_action on a
// single flag. Idempotent: re-resolving an already-resolved flag just
// overwrites the action (operators occasionally upgrade ignore→hide).
func (r *FlagRepository) Resolve(ctx context.Context, id, by string, action domain.FlagResolutionAction) error {
	return r.db(ctx, false).
		Model(&domain.OpportunityFlag{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"resolved_at":       time.Now().UTC(),
			"resolved_by":       by,
			"resolution_action": action,
		}).Error
}

// ResolveAllForOpportunity bulk-resolves every unresolved flag on a
// slug. Used by the ban_source action — when an operator decides the
// slug's source is bad, every flag pointing at that slug becomes
// "resolved by ban_source" in one shot.
func (r *FlagRepository) ResolveAllForOpportunity(ctx context.Context, slug, by string, action domain.FlagResolutionAction) (int64, error) {
	res := r.db(ctx, false).
		Model(&domain.OpportunityFlag{}).
		Where("opportunity_slug = ? AND resolved_at IS NULL", slug).
		Updates(map[string]any{
			"resolved_at":       time.Now().UTC(),
			"resolved_by":       by,
			"resolution_action": action,
		})
	return res.RowsAffected, res.Error
}

// TopFlaggedRow is a lightweight aggregate row returned by TopFlagged.
// Slug + count is enough for the admin dashboard; the operator clicks
// through to the full review surface for details.
type TopFlaggedRow struct {
	OpportunitySlug string `json:"opportunity_slug"`
	Kind            string `json:"kind"`
	FlagCount       int    `json:"flag_count"`
}

// TopFlagged returns the most-flagged opportunities (by unresolved
// flag count) for operator triage. Limit is clamped to [1, 200].
func (r *FlagRepository) TopFlagged(ctx context.Context, limit int) ([]TopFlaggedRow, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var rows []TopFlaggedRow
	err := r.db(ctx, true).
		Model(&domain.OpportunityFlag{}).
		Select("opportunity_slug, opportunity_kind AS kind, COUNT(*) AS flag_count").
		Where("resolved_at IS NULL").
		Group("opportunity_slug, opportunity_kind").
		Order("flag_count DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}
