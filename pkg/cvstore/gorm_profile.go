package cvstore

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// GormProfilePointers updates candidate_profiles CV columns via GORM
// (same access style as repository.CandidateRepository).
type GormProfilePointers struct {
	DB func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewGormProfilePointers constructs profile pointer updates.
func NewGormProfilePointers(db func(ctx context.Context, readOnly bool) *gorm.DB) *GormProfilePointers {
	return &GormProfilePointers{DB: db}
}

// SetCVPointers writes durable CV storage fields. No-op when the profile
// row does not exist yet (chat-first upload before onboard).
func (p *GormProfilePointers) SetCVPointers(ctx context.Context, candidateID, contentURI, contentHash, cvURL string) error {
	if p == nil || p.DB == nil {
		return fmt.Errorf("cvstore: db is nil")
	}
	if candidateID == "" {
		return fmt.Errorf("cvstore: candidate_id required")
	}
	updates := map[string]any{}
	if contentURI != "" {
		updates["cv_storage_uri"] = contentURI
	}
	if contentHash != "" {
		updates["cv_content_hash"] = contentHash
	}
	if cvURL != "" {
		updates["cv_url"] = cvURL
	}
	if len(updates) == 0 {
		return nil
	}
	res := p.DB(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ? OR profile_id = ?", candidateID, candidateID).
		Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("cvstore: set cv pointers: %w", res.Error)
	}
	return nil
}
