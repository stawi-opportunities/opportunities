package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// newFlagTestDB stands up a sqlite database with the opportunity_flags
// schema. Production runs Postgres; sqlite is sufficient for the
// repository's CRUD-shape tests because every query is portable
// (no JSONB, no array types).
func newFlagTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE opportunity_flags (
		id                 varchar(20) PRIMARY KEY,
		created_at         datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at         datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
		deleted_at         datetime,
		opportunity_slug   varchar(255) NOT NULL,
		opportunity_kind   varchar(40)  NOT NULL,
		submitted_by       varchar(64)  NOT NULL,
		reason             varchar(20)  NOT NULL,
		description        text,
		resolved_at        datetime,
		resolved_by        varchar(64)  NOT NULL DEFAULT '',
		resolution_action  varchar(20)  NOT NULL DEFAULT ''
	)`).Error; err != nil {
		t.Fatalf("create opportunity_flags: %v", err)
	}
	return db
}

func TestFlagRepo_CreateAndExists(t *testing.T) {
	db := newFlagTestDB(t)
	repo := NewFlagRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	flag := &domain.OpportunityFlag{
		OpportunitySlug: "abc",
		OpportunityKind: "job",
		SubmittedBy:     "user-1",
		Reason:          domain.FlagScam,
		Description:     "phishing site",
	}
	if err := repo.Create(ctx, flag); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if flag.ID == "" {
		t.Errorf("expected ID to be set, got empty")
	}

	exists, err := repo.ExistsForUser(ctx, "abc", "user-1")
	if err != nil {
		t.Fatalf("ExistsForUser: %v", err)
	}
	if !exists {
		t.Errorf("expected exists=true")
	}
	exists, err = repo.ExistsForUser(ctx, "abc", "other-user")
	if err != nil {
		t.Fatalf("ExistsForUser: %v", err)
	}
	if exists {
		t.Errorf("expected exists=false for other user")
	}
}

func TestFlagRepo_CountUnresolvedByOpportunity(t *testing.T) {
	db := newFlagTestDB(t)
	repo := NewFlagRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	for i, u := range []string{"u1", "u2", "u3"} {
		f := &domain.OpportunityFlag{
			OpportunitySlug: "abc",
			OpportunityKind: "job",
			SubmittedBy:     u,
			Reason:          domain.FlagScam,
		}
		if err := repo.Create(ctx, f); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	// Different reason should not count.
	_ = repo.Create(ctx, &domain.OpportunityFlag{
		OpportunitySlug: "abc", OpportunityKind: "job", SubmittedBy: "u4", Reason: domain.FlagSpam,
	})

	n, err := repo.CountUnresolvedByOpportunity(ctx, "abc")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 scam flags, got %d", n)
	}
}

func TestFlagRepo_ResolveAndResolveAll(t *testing.T) {
	db := newFlagTestDB(t)
	repo := NewFlagRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	for i, u := range []string{"u1", "u2", "u3"} {
		_ = i
		_ = repo.Create(ctx, &domain.OpportunityFlag{
			OpportunitySlug: "abc", OpportunityKind: "job", SubmittedBy: u, Reason: domain.FlagScam,
		})
	}
	flags, _ := repo.ListByOpportunity(ctx, "abc")
	if len(flags) != 3 {
		t.Fatalf("setup: expected 3 flags, got %d", len(flags))
	}

	if err := repo.Resolve(ctx, flags[0].ID, "op-1", domain.FlagActionIgnore); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, _ := repo.GetByID(ctx, flags[0].ID)
	if got.ResolvedAt == nil {
		t.Errorf("Resolve did not set resolved_at")
	}
	if got.ResolvedBy != "op-1" {
		t.Errorf("ResolvedBy=%q want op-1", got.ResolvedBy)
	}

	n, err := repo.ResolveAllForOpportunity(ctx, "abc", "op-1", domain.FlagActionBanSource)
	if err != nil {
		t.Fatalf("ResolveAll: %v", err)
	}
	if n != 2 {
		t.Errorf("ResolveAll affected %d, want 2", n)
	}

	count, _ := repo.CountUnresolvedByOpportunity(ctx, "abc")
	if count != 0 {
		t.Errorf("expected 0 unresolved after resolve all, got %d", count)
	}
}

func TestFlagRepo_TopFlagged(t *testing.T) {
	db := newFlagTestDB(t)
	repo := NewFlagRepository(func(_ context.Context, _ bool) *gorm.DB { return db })
	ctx := context.Background()

	_ = repo.Create(ctx, &domain.OpportunityFlag{OpportunitySlug: "a", OpportunityKind: "job", SubmittedBy: "u1", Reason: domain.FlagScam})
	_ = repo.Create(ctx, &domain.OpportunityFlag{OpportunitySlug: "a", OpportunityKind: "job", SubmittedBy: "u2", Reason: domain.FlagScam})
	_ = repo.Create(ctx, &domain.OpportunityFlag{OpportunitySlug: "b", OpportunityKind: "job", SubmittedBy: "u3", Reason: domain.FlagSpam})

	rows, err := repo.TopFlagged(ctx, 10)
	if err != nil {
		t.Fatalf("TopFlagged: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("expected at least one row")
	}
	if rows[0].OpportunitySlug != "a" {
		t.Errorf("expected top-flagged to be 'a', got %q (count=%d)", rows[0].OpportunitySlug, rows[0].FlagCount)
	}
	if rows[0].FlagCount != 2 {
		t.Errorf("flag_count=%d want 2", rows[0].FlagCount)
	}
}
