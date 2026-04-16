package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
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
func (r *CandidateRepository) GetByID(ctx context.Context, id int64) (*domain.CandidateProfile, error) {
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

// GetByEmail retrieves a candidate profile by email address.
// Returns nil, nil if no record is found.
func (r *CandidateRepository) GetByEmail(ctx context.Context, email string) (*domain.CandidateProfile, error) {
	var c domain.CandidateProfile
	err := r.db(ctx, true).Where("email = ?", email).First(&c).Error
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
func (r *CandidateRepository) UpdateStatus(ctx context.Context, id int64, status domain.CandidateStatus) error {
	return r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// UpdateEmbedding stores a JSON-encoded embedding vector for the given candidate.
func (r *CandidateRepository) UpdateEmbedding(ctx context.Context, id int64, embedding string) error {
	return r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Update("embedding", embedding).Error
}

// IncrementMatchesSent increments the matches_sent counter and updates last_contacted_at.
func (r *CandidateRepository) IncrementMatchesSent(ctx context.Context, id int64) error {
	now := time.Now()
	return r.db(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"matches_sent":     gorm.Expr("matches_sent + 1"),
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
