package cvstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// GormIndexStore is the GORM-backed local CV document index.
// Mirrors repository.CandidateRepository style: db(ctx, readOnly) *gorm.DB.
type GormIndexStore struct {
	DB func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewGormIndexStore constructs a CV document repository.
func NewGormIndexStore(db func(ctx context.Context, readOnly bool) *gorm.DB) *GormIndexStore {
	return &GormIndexStore{DB: db}
}

// UpsertDocument writes/replaces the current CV document and returns the version.
func (s *GormIndexStore) UpsertDocument(ctx context.Context, doc Document) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("cvstore: db is nil")
	}
	if doc.CandidateID == "" {
		return 0, fmt.Errorf("cvstore: candidate_id required")
	}

	var prev matching.CandidateCVDocumentRecord
	err := s.DB(ctx, true).
		Where("candidate_id = ?", doc.CandidateID).
		First(&prev).Error

	version := 1
	switch {
	case err == gorm.ErrRecordNotFound:
		version = 1
	case err != nil:
		return 0, fmt.Errorf("cvstore: load prior: %w", err)
	default:
		if prev.ContentHash != "" && prev.ContentHash == doc.ContentHash {
			version = prev.Version
		} else {
			version = prev.Version + 1
		}
	}
	if version < 1 {
		version = 1
	}

	row := matching.CandidateCVDocumentRecord{
		CandidateID:   doc.CandidateID,
		Version:       version,
		FileID:        doc.FileID,
		ContentURI:    doc.ContentURI,
		ContentHash:   doc.ContentHash,
		Filename:      doc.Filename,
		ContentType:   doc.ContentType,
		SizeBytes:     doc.SizeBytes,
		ExtractedText: doc.ExtractedText,
		TextLength:    doc.TextLength,
		Storage:       doc.Storage,
		UpdatedAt:     time.Now().UTC(),
	}
	if err := s.DB(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "candidate_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"version", "file_id", "content_uri", "content_hash",
				"filename", "content_type", "size_bytes", "extracted_text",
				"text_length", "storage", "updated_at",
			}),
		}).
		Create(&row).Error; err != nil {
		return 0, fmt.Errorf("cvstore: upsert document: %w", err)
	}
	return version, nil
}

// GetCurrent returns the latest document, or nil when none exists.
func (s *GormIndexStore) GetCurrent(ctx context.Context, candidateID string) (*Document, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("cvstore: db is nil")
	}
	var row matching.CandidateCVDocumentRecord
	err := s.DB(ctx, true).
		Where("candidate_id = ?", candidateID).
		First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cvstore: get current: %w", err)
	}
	return &Document{
		CandidateID:   row.CandidateID,
		Version:       row.Version,
		FileID:        row.FileID,
		ContentURI:    row.ContentURI,
		ContentHash:   row.ContentHash,
		Filename:      row.Filename,
		ContentType:   row.ContentType,
		SizeBytes:     row.SizeBytes,
		ExtractedText: row.ExtractedText,
		TextLength:    row.TextLength,
		Storage:       row.Storage,
		UpdatedAt:     row.UpdatedAt,
	}, nil
}
