// Package setupmode decides whether a process should run its one-shot
// migrate/setup work and exit instead of serving.
package setupmode

import (
	"os"
	"strconv"

	"github.com/pitabwire/frame/v2"
)

// LegacyMigrateEnv is the pre-Frame-setup variable that deploy manifests and
// runbooks used. Frame itself reads DO_MIGRATION / DO_SETUP, so without this
// shim a Job that only sets DO_DATABASE_MIGRATE boots the full server.
const LegacyMigrateEnv = "DO_DATABASE_MIGRATE"

// ShouldMigrate reports whether cfg (a Frame config) is in setup/migrate mode:
// argv "setup"/"migrate", DO_SETUP, DO_MIGRATION, or legacy DO_DATABASE_MIGRATE.
func ShouldMigrate(cfg any) bool {
	if frame.ShouldRunSetup(cfg) {
		return true
	}
	v, err := strconv.ParseBool(os.Getenv(LegacyMigrateEnv))
	return err == nil && v
}
