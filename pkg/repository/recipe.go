package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/xid"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"gorm.io/gorm"
)

// SourceRecipe is one row of the source_recipes history table.
type SourceRecipe struct {
	ID               string          `gorm:"column:id;primaryKey"`
	SourceID         string          `gorm:"column:source_id"`
	Version          int             `gorm:"column:version"`
	Recipe           json.RawMessage `gorm:"column:recipe;type:jsonb"`
	Status           string          `gorm:"column:status"`
	PassRate         float64         `gorm:"column:pass_rate"`
	Model            string          `gorm:"column:model"`
	ValidationReport json.RawMessage `gorm:"column:validation_report;type:jsonb"`
	CreatedAt        time.Time       `gorm:"column:created_at"`
}

func (SourceRecipe) TableName() string { return "source_recipes" }

// RecipeRepository persists per-source extraction recipes: the active recipe
// inline on the source plus full version history with atomic swap and rollback.
type RecipeRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewRecipeRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *RecipeRepository {
	return &RecipeRepository{db: db}
}

// Active returns the source's active recipe, or nil when it has none. An empty
// JSON object ('{}', i.e. Acquisition unset) counts as "none".
func (r *RecipeRepository) Active(ctx context.Context, sourceID string) (*recipe.Recipe, error) {
	var src domain.Source
	if err := r.db(ctx, true).Select("extraction_recipe").Where("id = ?", sourceID).First(&src).Error; err != nil {
		return nil, err
	}
	return decodeRecipe(src.ExtractionRecipe)
}

// decodeRecipe parses a recipe JSON string, returning nil for an empty/unset one.
func decodeRecipe(s string) (*recipe.Recipe, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var rec recipe.Recipe
	if err := json.Unmarshal([]byte(s), &rec); err != nil {
		return nil, fmt.Errorf("decode recipe: %w", err)
	}
	if rec.Acquisition == "" {
		return nil, nil
	}
	return &rec, nil
}

// Activate atomically installs rec as the source's active recipe: it inserts a
// new history row (version = prior max + 1) as 'active', marks any prior active
// row 'superseded', and updates sources.extraction_recipe — all in one tx. The
// recipe's Version field is set to the assigned version.
func (r *RecipeRepository) Activate(ctx context.Context, sourceID string, rec *recipe.Recipe, passRate float64, model string, validationReport any) error {
	recJSON, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal recipe: %w", err)
	}
	var reportJSON json.RawMessage
	if validationReport != nil {
		if reportJSON, err = json.Marshal(validationReport); err != nil {
			return fmt.Errorf("marshal validation report: %w", err)
		}
	}
	return r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		var maxV *int
		if err := tx.Model(&SourceRecipe{}).Where("source_id = ?", sourceID).
			Select("MAX(version)").Scan(&maxV).Error; err != nil {
			return err
		}
		next := 1
		if maxV != nil {
			next = *maxV + 1
		}
		rec.Version = next
		// Re-marshal so the stored JSON carries the assigned version.
		if recJSON, err = json.Marshal(rec); err != nil {
			return err
		}
		if err := tx.Model(&SourceRecipe{}).
			Where("source_id = ? AND status = ?", sourceID, "active").
			Update("status", "superseded").Error; err != nil {
			return err
		}
		row := &SourceRecipe{
			ID: xid.New().String(), SourceID: sourceID, Version: next,
			Recipe: recJSON, Status: "active", PassRate: passRate, Model: model,
			ValidationReport: reportJSON, CreatedAt: time.Now().UTC(),
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return tx.Model(&domain.Source{}).Where("id = ?", sourceID).
			Update("extraction_recipe", json.RawMessage(recJSON)).Error
	})
}

// Rollback re-activates a prior version: marks the current active row
// superseded, the target version active, and copies its recipe back inline.
func (r *RecipeRepository) Rollback(ctx context.Context, sourceID string, version int) error {
	return r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		var target SourceRecipe
		if err := tx.Where("source_id = ? AND version = ?", sourceID, version).First(&target).Error; err != nil {
			return fmt.Errorf("rollback target v%d: %w", version, err)
		}
		if err := tx.Model(&SourceRecipe{}).Where("source_id = ? AND status = ?", sourceID, "active").
			Update("status", "superseded").Error; err != nil {
			return err
		}
		if err := tx.Model(&SourceRecipe{}).Where("id = ?", target.ID).
			Update("status", "active").Error; err != nil {
			return err
		}
		return tx.Model(&domain.Source{}).Where("id = ?", sourceID).
			Update("extraction_recipe", target.Recipe).Error
	})
}

// History returns all recipe versions for a source, newest first.
func (r *RecipeRepository) History(ctx context.Context, sourceID string) ([]SourceRecipe, error) {
	var rows []SourceRecipe
	err := r.db(ctx, true).Where("source_id = ?", sourceID).Order("version DESC").Find(&rows).Error
	return rows, err
}
