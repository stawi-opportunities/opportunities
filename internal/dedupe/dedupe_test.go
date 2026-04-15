package dedupe

import (
	"context"
	"testing"
	"time"

	"stawi.jobs/internal/domain"
	"stawi.jobs/internal/storage"
)

func TestUpsertAndCluster(t *testing.T) {
	store := storage.NewMemoryStore()
	engine := New(store)
	ctx := context.Background()
	variant := domain.JobVariant{
		ExternalJobID: "abc",
		SourceID:      1,
		Title:         "Go Engineer",
		Company:       "Acme",
		LocationText:  "Remote",
		Description:   "work on systems",
		ScrapedAt:     time.Now().UTC(),
	}
	canonical, err := engine.UpsertAndCluster(ctx, variant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if canonical.ClusterID == 0 {
		t.Fatal("expected cluster id")
	}
}
