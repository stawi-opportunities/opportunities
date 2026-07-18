package placement

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// GormStore persists placement profiles via GORM (matching-package model).
type GormStore struct {
	DB func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewGormStore constructs a placement profile repository.
func NewGormStore(db func(ctx context.Context, readOnly bool) *gorm.DB) *GormStore {
	return &GormStore{DB: db}
}

// Upsert writes the placement document and increments version when summary changes.
func (s *GormStore) Upsert(ctx context.Context, doc Document) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("placement: db is nil")
	}
	if doc.CandidateID == "" {
		return 0, fmt.Errorf("placement: candidate_id required")
	}

	var prev matching.CandidatePlacementProfileRecord
	err := s.DB(ctx, true).
		Where("candidate_id = ?", doc.CandidateID).
		First(&prev).Error

	version := 1
	switch {
	case err == gorm.ErrRecordNotFound:
		// first version remains 1
	case err != nil:
		return 0, fmt.Errorf("placement: load prior: %w", err)
	default:
		if prev.SummaryText == doc.SummaryText {
			version = prev.Version
		} else {
			version = prev.Version + 1
		}
	}

	row := matching.CandidatePlacementProfileRecord{
		CandidateID:        doc.CandidateID,
		Version:            version,
		SummaryText:        doc.SummaryText,
		QualificationsText: doc.QualificationsText,
		PreferencesText:    doc.PreferencesText,
		ConversationDigest: doc.ConversationDigest,
		Missing:            pq.StringArray(doc.Missing),
		Ready:              doc.Ready,
		UpdatedAt:          time.Now().UTC(),
	}
	if err := s.DB(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "candidate_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"version", "summary_text", "qualifications_text", "preferences_text",
				"conversation_digest", "missing", "ready", "updated_at",
			}),
		}).
		Create(&row).Error; err != nil {
		return 0, fmt.Errorf("placement: upsert: %w", err)
	}
	return version, nil
}

// Get returns the current placement profile or nil.
func (s *GormStore) Get(ctx context.Context, candidateID string) (*Document, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("placement: db is nil")
	}
	var row matching.CandidatePlacementProfileRecord
	err := s.DB(ctx, true).
		Where("candidate_id = ?", candidateID).
		First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("placement: get: %w", err)
	}
	return &Document{
		CandidateID:        row.CandidateID,
		Version:            row.Version,
		SummaryText:        row.SummaryText,
		QualificationsText: row.QualificationsText,
		PreferencesText:    row.PreferencesText,
		ConversationDigest: row.ConversationDigest,
		ContentHash:        ContentHash(row.SummaryText),
		RerankText:         RerankText(matchHeadlineFromSummary(row.SummaryText), row.PreferencesText, row.ConversationDigest, 1800),
		Missing:            []string(row.Missing),
		Ready:              row.Ready,
	}, nil
}

func matchHeadlineFromSummary(summary string) string {
	// Best-effort: first non-empty line after "## Match headline".
	const marker = "## Match headline"
	i := strings.Index(summary, marker)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(summary[i+len(marker):])
	if line, _, ok := strings.Cut(rest, "\n"); ok {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(rest)
}
