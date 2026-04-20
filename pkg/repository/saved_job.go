package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
)

type SavedJobRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewSavedJobRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *SavedJobRepository {
	return &SavedJobRepository{db: db}
}

func (r *SavedJobRepository) Save(ctx context.Context, profileID string, jobID string) error {
	sj := domain.SavedJob{
		ProfileID:      profileID,
		CanonicalJobID: jobID,
		SavedAt:        time.Now(),
	}
	result := r.db(ctx, false).
		Where("profile_id = ? AND canonical_job_id = ?", profileID, jobID).
		FirstOrCreate(&sj)
	return result.Error
}

func (r *SavedJobRepository) Delete(ctx context.Context, profileID string, jobID string) error {
	return r.db(ctx, false).
		Where("profile_id = ? AND canonical_job_id = ?", profileID, jobID).
		Delete(&domain.SavedJob{}).Error
}

func (r *SavedJobRepository) ListForProfile(ctx context.Context, profileID string, limit int) ([]domain.SavedJob, error) {
	var jobs []domain.SavedJob
	err := r.db(ctx, true).
		Where("profile_id = ?", profileID).
		Order("saved_at DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

func (r *SavedJobRepository) Exists(ctx context.Context, profileID string, jobID string) (bool, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.SavedJob{}).
		Where("profile_id = ? AND canonical_job_id = ?", profileID, jobID).
		Count(&count).Error
	return count > 0, err
}

// ListProfileIDsByCanonicalJob returns the distinct profile_ids that
// have saved a given canonical job. Used by the expired-job
// notification fan-out: when the redirect service marks a link
// dead, every profile here gets a "posting removed" email.
func (r *SavedJobRepository) ListProfileIDsByCanonicalJob(ctx context.Context, canonicalJobID string) ([]string, error) {
	var ids []string
	err := r.db(ctx, true).Model(&domain.SavedJob{}).
		Where("canonical_job_id = ?", canonicalJobID).
		Distinct("profile_id").
		Pluck("profile_id", &ids).Error
	return ids, err
}
