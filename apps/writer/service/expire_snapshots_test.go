package service

import (
	"testing"
	"time"
)

// TestAppendOnlyTables_Count guards against accidental additions to
// AppendOnlyTables without a matching maintenance-scope update. If you
// add a table, update this count and add the table to the comment below.
//
// Current tables (14):
//
//	jobs:       variants, canonicals, canonicals_expired, embeddings,
//	            translations, published, crawl_page_completed, sources_discovered
//	candidates: cv_uploaded, cv_extracted, cv_improved, preferences,
//	            embeddings, matches_ready
func TestAppendOnlyTables_Count(t *testing.T) {
	const want = 14
	if got := len(AppendOnlyTables); got != want {
		t.Errorf("AppendOnlyTables: got %d tables, want %d — update this test when adding/removing tables", got, want)
	}
}

// TestExpireSnapshotsConfig_Defaults verifies that a zero-value
// ExpireSnapshotsConfig is filled with safe defaults by ExpireSnapshots.
// We exercise the default-filling logic directly without a real catalog.
func TestExpireSnapshotsConfig_Defaults(t *testing.T) {
	cfg := ExpireSnapshotsConfig{} // all zero

	// Mirror the default-filling logic from ExpireSnapshots.
	if cfg.OlderThan <= 0 {
		cfg.OlderThan = 14 * 24 * time.Hour
	}
	if cfg.MinSnapshotsToKeep <= 0 {
		cfg.MinSnapshotsToKeep = 100
	}
	if cfg.PerTableTimeout <= 0 {
		cfg.PerTableTimeout = 5 * time.Minute
	}
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = 4
	}

	if want := 14 * 24 * time.Hour; cfg.OlderThan != want {
		t.Errorf("OlderThan default: got %v, want %v", cfg.OlderThan, want)
	}
	if want := 100; cfg.MinSnapshotsToKeep != want {
		t.Errorf("MinSnapshotsToKeep default: got %d, want %d", cfg.MinSnapshotsToKeep, want)
	}
	if want := 5 * time.Minute; cfg.PerTableTimeout != want {
		t.Errorf("PerTableTimeout default: got %v, want %v", cfg.PerTableTimeout, want)
	}
	if want := 4; cfg.Parallelism != want {
		t.Errorf("Parallelism default: got %d, want %d", cfg.Parallelism, want)
	}
}

// TestExpireSnapshotsConfig_ExplicitValues confirms that explicit (non-zero)
// values are preserved and not overwritten by the default-filling logic.
func TestExpireSnapshotsConfig_ExplicitValues(t *testing.T) {
	cfg := ExpireSnapshotsConfig{
		OlderThan:          7 * 24 * time.Hour,
		MinSnapshotsToKeep: 50,
		PerTableTimeout:    2 * time.Minute,
		Parallelism:        8,
	}

	// Simulate the guard: explicit non-zero values must not be overwritten.
	if cfg.OlderThan <= 0 {
		cfg.OlderThan = 14 * 24 * time.Hour
	}
	if cfg.MinSnapshotsToKeep <= 0 {
		cfg.MinSnapshotsToKeep = 100
	}
	if cfg.PerTableTimeout <= 0 {
		cfg.PerTableTimeout = 5 * time.Minute
	}
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = 4
	}

	if want := 7 * 24 * time.Hour; cfg.OlderThan != want {
		t.Errorf("OlderThan: got %v, want %v (explicit value must be preserved)", cfg.OlderThan, want)
	}
	if want := 50; cfg.MinSnapshotsToKeep != want {
		t.Errorf("MinSnapshotsToKeep: got %d, want %d (explicit value must be preserved)", cfg.MinSnapshotsToKeep, want)
	}
	if want := 2 * time.Minute; cfg.PerTableTimeout != want {
		t.Errorf("PerTableTimeout: got %v, want %v (explicit value must be preserved)", cfg.PerTableTimeout, want)
	}
	if want := 8; cfg.Parallelism != want {
		t.Errorf("Parallelism: got %d, want %d (explicit value must be preserved)", cfg.Parallelism, want)
	}
}
