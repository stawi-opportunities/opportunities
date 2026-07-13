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

	"github.com/lib/pq"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe/stock"
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
	SourceType domain.SourceType `json:"source_type"`
	BaseURL    string            `json:"base_url"`
	// Name is an optional operator-facing label.
	Name    string `json:"name,omitempty"`
	Country string `json:"country"`
	// Language is the ISO 639-1 code of postings served by this source
	// (e.g. "en", "fr", "ja"). Blank entries default to "en".
	Language         string        `json:"language"`
	Region           string        `json:"region"`
	CrawlIntervalSec int           `json:"crawl_interval_sec"`
	Priority         priorityLabel `json:"priority"`
	// Status when set ("active", "paused", …) overrides the default
	// active seed status. Use "paused" for hostile boards that must not
	// crawl until a recipe + compliance review exist.
	Status string `json:"status,omitempty"`
	// Kinds declares which opportunity kinds this seeded source emits.
	// Blank entries default to ["job"].
	Kinds []string `json:"kinds,omitempty"`
	// RequiredAttributesByKind tightens Spec.KindRequired for this source.
	// Optional; blank entries default to {}.
	RequiredAttributesByKind map[string][]string `json:"required_attributes_by_kind,omitempty"`
	// Recipe is a stock recipe name under definitions/stock-recipes/
	// (e.g. "remoteok"). When set (or when host matches a stock recipe),
	// the seed path installs that recipe onto the source after upsert.
	Recipe string `json:"recipe,omitempty"`
	// ListingPath is the definite jobs listing relative to BaseURL.
	ListingPath string `json:"listing_path,omitempty"`
}

// LoadAndUpsert walks seedsDir recursively, reads every .json file, unmarshals
// the entries into []SeedEntry, and upserts each one as a domain.Source.
// It returns the total number of entries processed (not unique inserts).
//
// reg may be nil — when nil, kind validation is skipped (useful for tests
// that don't load the opportunity registry). In production reg should
// always be non-nil so seeds with unknown kinds are caught at boot.
//
// recipes may be nil; when non-nil, stock recipes named in the seed (or
// matched by base_url host) are activated onto the source.
func LoadAndUpsert(ctx context.Context, seedsDir string, repo *repository.SourceRepository, reg *opportunity.Registry) (int, error) {
	return LoadAndUpsertWithRecipes(ctx, seedsDir, repo, reg, nil)
}

// LoadAndUpsertWithRecipes is LoadAndUpsert plus optional stock-recipe attach.
func LoadAndUpsertWithRecipes(ctx context.Context, seedsDir string, repo *repository.SourceRepository, reg *opportunity.Registry, recipes *repository.RecipeRepository) (int, error) {
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

			kinds := e.Kinds
			if len(kinds) == 0 {
				kinds = []string{"job"}
			}
			if reg != nil {
				for _, k := range kinds {
					if _, ok := reg.Lookup(k); !ok {
						return fmt.Errorf("%s entry %d (%s %s): unknown kind %q (known: %v)",
							path, i, e.SourceType, e.BaseURL, k, reg.Known())
					}
				}
			}

			reqByKind := e.RequiredAttributesByKind
			if reqByKind == nil {
				reqByKind = map[string][]string{}
			}

			now := time.Now().UTC()
			lang := strings.ToLower(strings.TrimSpace(e.Language))
			if lang == "" {
				lang = "en"
			}
			status := domain.SourceActive
			switch strings.ToLower(strings.TrimSpace(e.Status)) {
			case "paused":
				status = domain.SourcePaused
			case "pending":
				status = domain.SourcePending
			case "degraded":
				status = domain.SourceDegraded
			case "active", "":
				status = domain.SourceActive
			}
			// Cap seed intervals to the production min crawl floor (12h)
			// so overdue sweeps and Trustage schedules stay aligned.
			interval := e.CrawlIntervalSec
			if interval > 0 && interval < 12*3600 {
				interval = 12 * 3600
			}
			// Prefer engine types for new seeds; legacy types still accepted
			// (crawl maps them via domain.EngineType / stock recipes).
			src := &domain.Source{
				Type:                     e.SourceType,
				Name:                     e.Name,
				BaseURL:                  e.BaseURL,
				Country:                  e.Country,
				Language:                 lang,
				Status:                   status,
				Priority:                 prio,
				CrawlIntervalSec:         interval,
				HealthScore:              1.0,
				Config:                   "{}",
				NextCrawlAt:              now,
				Kinds:                    pq.StringArray(kinds),
				RequiredAttributesByKind: reqByKind,
				ListingPath:              e.ListingPath,
			}

			if err := repo.Upsert(ctx, src); err != nil {
				return fmt.Errorf("upsert %s entry %d (%s %s): %w",
					path, i, e.SourceType, e.BaseURL, err)
			}
			if recipes != nil {
				if err := attachSeedRecipe(ctx, recipes, repo, src, e.Recipe); err != nil {
					return fmt.Errorf("%s entry %d recipe: %w", path, i, err)
				}
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

// attachSeedRecipe installs a stock recipe when the source has none yet.
// Recipe name comes from the seed field, then host lookup.
func attachSeedRecipe(ctx context.Context, recipes *repository.RecipeRepository, sources *repository.SourceRepository, seed *domain.Source, recipeName string) error {
	// Resolve the row id after upsert (unique on type+base_url).
	row, err := sources.GetByTypeAndURL(ctx, seed.Type, seed.BaseURL)
	if err != nil {
		return err
	}
	if row == nil {
		return nil
	}
	active, err := recipes.Active(ctx, row.ID)
	if err != nil {
		return err
	}
	if active != nil {
		return nil
	}
	name := strings.TrimSpace(recipeName)
	var rec = stock.Get(name)
	if rec == nil {
		name, rec = stock.LookupByBaseURL(seed.BaseURL)
	}
	if rec == nil {
		name, rec = stock.LookupLegacyType(string(seed.Type))
	}
	if rec == nil {
		return nil
	}
	if err := recipes.Activate(ctx, row.ID, rec, 1.0, "stock:"+name, map[string]any{
		"source": "seed", "stock": name,
	}); err != nil {
		return err
	}
	util.Log(ctx).WithField("source_id", row.ID).WithField("stock", name).
		Info("seeds: stock recipe activated")
	return nil
}
