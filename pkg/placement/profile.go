package placement

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// ProfileCV is the durable file reference stored on the candidate profile.
type ProfileCV struct {
	FileID      string
	ContentURI  string
	ContentHash string
	CVURL       string
}

// ProfileStore updates candidate_profiles CV file references (GORM).
type ProfileStore interface {
	// SetCVFileRef stores the files-service id / URI on the profile.
	// No-op when the profile row does not exist yet (chat before onboard).
	SetCVFileRef(ctx context.Context, candidateID string, ref ProfileCV) error
	// GetCVFileRef returns stored file pointers, or empty when absent.
	GetCVFileRef(ctx context.Context, candidateID string) (ProfileCV, error)
}

// GormProfileStore implements ProfileStore via GORM.
type GormProfileStore struct {
	DB func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewGormProfileStore constructs a profile CV pointer repository.
func NewGormProfileStore(db func(ctx context.Context, readOnly bool) *gorm.DB) *GormProfileStore {
	return &GormProfileStore{DB: db}
}

// SetCVFileRef writes cv_storage_uri (file id preferred), cv_content_hash, cv_url.
func (p *GormProfileStore) SetCVFileRef(ctx context.Context, candidateID string, ref ProfileCV) error {
	if p == nil || p.DB == nil {
		return fmt.Errorf("placement: db is nil")
	}
	if candidateID == "" {
		return fmt.Errorf("placement: candidate_id required")
	}
	// Prefer files media id as the durable reference; URI for display/download.
	storage := ref.FileID
	if storage == "" {
		storage = ref.ContentURI
	}
	url := ref.CVURL
	if url == "" {
		url = ref.ContentURI
	}
	updates := map[string]any{}
	if storage != "" {
		updates["cv_storage_uri"] = storage
	}
	if ref.ContentHash != "" {
		updates["cv_content_hash"] = ref.ContentHash
	}
	if url != "" {
		updates["cv_url"] = url
	}
	if len(updates) == 0 {
		return nil
	}
	res := p.DB(ctx, false).
		Model(&domain.CandidateProfile{}).
		Where("id = ? OR profile_id = ?", candidateID, candidateID).
		Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("placement: set cv file ref: %w", res.Error)
	}
	return nil
}

// GetCVFileRef loads file pointers from the profile.
func (p *GormProfileStore) GetCVFileRef(ctx context.Context, candidateID string) (ProfileCV, error) {
	if p == nil || p.DB == nil {
		return ProfileCV{}, fmt.Errorf("placement: db is nil")
	}
	var c domain.CandidateProfile
	err := p.DB(ctx, true).
		Select("cv_storage_uri", "cv_content_hash", "cv_url").
		Where("id = ? OR profile_id = ?", candidateID, candidateID).
		First(&c).Error
	if err == gorm.ErrRecordNotFound {
		return ProfileCV{}, nil
	}
	if err != nil {
		return ProfileCV{}, fmt.Errorf("placement: get cv file ref: %w", err)
	}
	// cv_storage_uri holds media id when files service was used.
	return ProfileCV{
		FileID:      c.CVStorageURI,
		ContentURI:  c.CVUrl,
		ContentHash: c.CVContentHash,
		CVURL:       c.CVUrl,
	}, nil
}
