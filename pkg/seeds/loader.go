package seeds

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// priorityLabel is a JSON-friendly alias for domain.Priority that accepts
// human-readable strings ("low", "normal", "hot", "urgent").
type priorityLabel string

func (p priorityLabel) toDomain() (domain.Priority, error) {
	switch strings.ToLower(string(p)) {
	case "low":
		return domain.PriorityLow, nil
	case "normal":
		return domain.PriorityNormal, nil
	case "hot", "high":
		return domain.PriorityHigh, nil
	case "urgent":
		return domain.PriorityUrgent, nil
	default:
		return domain.PriorityNormal, fmt.Errorf("unknown priority label %q", p)
	}
}

// SeedEntry is the JSON shape used in every seeds/**/*.json file.
type SeedEntry struct {
	SourceType       domain.SourceType `json:"source_type"`
	BaseURL          string            `json:"base_url"`
	Country          string            `json:"country"`
	// Language is the ISO 639-1 code of postings served by this source
	// (e.g. "en", "fr", "ja"). Blank entries default to "en" — almost
	// every legacy seed predates this field.
	Language         string            `json:"language"`
	Region           string            `json:"region"`
	CrawlIntervalSec int               `json:"crawl_interval_sec"`
	Priority         priorityLabel     `json:"priority"`
}

// LoadAndUpsert walks seedsDir recursively, reads every .json file, unmarshals
// the entries into []SeedEntry, and upserts each one as a domain.Source.
// It returns the total number of entries processed (not unique inserts).
func LoadAndUpsert(ctx context.Context, seedsDir string, repo *repository.SourceRepository) (int, error) {
	total := 0

	err := filepath.WalkDir(seedsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".json") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		var entries []SeedEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("unmarshal %s: %w", path, err)
		}

		for i, e := range entries {
			prio, err := e.Priority.toDomain()
			if err != nil {
				return fmt.Errorf("%s entry %d: %w", path, i, err)
			}

			now := time.Now().UTC()
			lang := strings.ToLower(strings.TrimSpace(e.Language))
			if lang == "" {
				lang = "en"
			}
			src := &domain.Source{
				Type:             e.SourceType,
				BaseURL:          e.BaseURL,
				Country:          e.Country,
				Language:         lang,
				Status:           domain.SourceActive,
				Priority:         prio,
				CrawlIntervalSec: e.CrawlIntervalSec,
				HealthScore:      1.0,
				Config:           "{}",
				NextCrawlAt:      now,
			}

			if err := repo.Upsert(ctx, src); err != nil {
				return fmt.Errorf("upsert %s entry %d (%s %s): %w",
					path, i, e.SourceType, e.BaseURL, err)
			}
			total++
		}
		return nil
	})

	if err != nil {
		return total, err
	}
	return total, nil
}
