package repository

import (
	"fmt"

	"gorm.io/gorm"
)

// FinalizeSchema handles the Postgres-specific schema tweaks that GORM's
// AutoMigrate cannot express.
//
// Call this once immediately after db.AutoMigrate(). Every step is idempotent
// and safe to re-run on every boot.
func FinalizeSchema(db *gorm.DB) error {
	steps := []struct {
		name string
		sql  string
	}{
		// pg_trgm extension — required for future fuzzy-search indexes.
		{"extension pg_trgm", `CREATE EXTENSION IF NOT EXISTS pg_trgm`},
		// One active recipe per source. AutoMigrate creates source_recipes
		// from the SourceRecipe struct, but GORM tags can't express a
		// WHERE-filtered (partial) unique index, so it lives here.
		{"source_recipes active uniq", `CREATE UNIQUE INDEX IF NOT EXISTS idx_source_recipes_active ON source_recipes (source_id) WHERE status = 'active'`},
	}

	for _, s := range steps {
		if err := db.Exec(s.sql).Error; err != nil {
			return fmt.Errorf("finalize schema step %q: %w", s.name, err)
		}
	}
	return nil
}
