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
	}

	for _, s := range steps {
		if err := db.Exec(s.sql).Error; err != nil {
			return fmt.Errorf("finalize schema step %q: %w", s.name, err)
		}
	}
	return nil
}
