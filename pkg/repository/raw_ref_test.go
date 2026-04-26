package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// domain.BaseModel uses `default:now()` for CreatedAt/UpdatedAt,
	// which is Postgres-specific. SQLite rejects that DDL, so we
	// create the table with a hand-rolled schema that mirrors the
	// columns we exercise. The production migration path is Postgres
	// via gorm.io/driver/postgres and that's what ships; this table
	// exists purely so the repository's queries have somewhere to run.
	if err := db.Exec(`CREATE TABLE raw_refs (
		id           varchar(20) PRIMARY KEY,
		created_at   datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at   datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		deleted_at   datetime,
		content_hash varchar(64) NOT NULL,
		cluster_id   varchar(20) NOT NULL,
		variant_id   varchar(20) NOT NULL,
		UNIQUE (content_hash, variant_id)
	)`).Error; err != nil {
		t.Fatalf("create raw_refs: %v", err)
	}
	if err := db.Exec(`CREATE INDEX idx_raw_refs_cluster ON raw_refs(cluster_id)`).Error; err != nil {
		t.Fatalf("create index: %v", err)
	}
	if err := db.Exec(`CREATE INDEX idx_raw_refs_deleted ON raw_refs(deleted_at)`).Error; err != nil {
		t.Fatalf("create index: %v", err)
	}
	_ = domain.RawRef{} // anchor the import
	return db
}

func TestRawRef_UpsertAndCount(t *testing.T) {
	db := newTestDB(t)
	repo := NewRawRefRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	if err := repo.Upsert(ctx, "hash1", "cluster1", "variant1"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Duplicate is a no-op.
	if err := repo.Upsert(ctx, "hash1", "cluster1", "variant1"); err != nil {
		t.Fatalf("Upsert dup: %v", err)
	}

	n, err := repo.CountByHash(ctx, "hash1")
	if err != nil {
		t.Fatalf("CountByHash: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	// Second variant sharing the same hash.
	_ = repo.Upsert(ctx, "hash1", "cluster2", "variant2")
	n, _ = repo.CountByHash(ctx, "hash1")
	if n != 2 {
		t.Errorf("count after second variant = %d, want 2", n)
	}
}

func TestRawRef_DeleteByCluster_ReturnsOrphanHashes(t *testing.T) {
	db := newTestDB(t)
	repo := NewRawRefRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	_ = repo.Upsert(ctx, "hash1", "cluster1", "variant1")
	_ = repo.Upsert(ctx, "hash1", "cluster2", "variant2") // shared
	_ = repo.Upsert(ctx, "hash2", "cluster1", "variant3") // orphan after deletion

	orphans, err := repo.DeleteByCluster(ctx, "cluster1")
	if err != nil {
		t.Fatalf("DeleteByCluster: %v", err)
	}
	// hash2 was ONLY referenced by cluster1, so it's orphaned.
	// hash1 is still referenced by cluster2, so it stays.
	if len(orphans) != 1 || orphans[0] != "hash2" {
		t.Errorf("orphans = %v, want [hash2]", orphans)
	}
}
