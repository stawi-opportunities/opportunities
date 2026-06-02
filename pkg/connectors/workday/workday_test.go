package workday

import (
	"context"
	"encoding/json"
	"testing"

	connectors "github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// TestCrawlResume_StartsFromCheckpointPage mirrors the greenhouse
// test: resume at page>1 short-circuits the HTTP fetch.
func TestCrawlResume_StartsFromCheckpointPage(t *testing.T) {
	c := &Connector{client: nil}
	cursor, _ := json.Marshal(struct {
		Page int `json:"page"`
	}{Page: 3})
	cp := &connectors.CheckpointState{
		Cursor:  cursor,
		PageIdx: 3,
		LastURL: "https://example.wd1.myworkdayjobs.com/wday/cxs/jobs",
	}

	it := c.CrawlResume(context.Background(), domain.Source{BaseURL: "https://example.wd1.myworkdayjobs.com"}, cp)
	if it.Next(context.Background()) {
		t.Fatal("resume past page 1 should yield no items")
	}
	if it.Err() != nil {
		t.Fatalf("clean resume should not return error, got %v", it.Err())
	}

	wIt, ok := it.(*iter)
	if !ok {
		t.Fatalf("expected *iter, got %T", it)
	}
	if wIt.page != 3 {
		t.Fatalf("iter.page = %d; want 3", wIt.page)
	}
}

// TestCheckpoint_RoundTrip verifies the iter Checkpoint() output can
// be unmarshalled back into a CrawlResume call.
func TestCheckpoint_RoundTrip(t *testing.T) {
	it := &iter{page: 5, lastURL: "https://example/bar"}
	cp := it.Checkpoint()
	if cp == nil {
		t.Fatal("Checkpoint returned nil")
	}
	if cp.PageIdx != 5 {
		t.Fatalf("PageIdx = %d; want 5", cp.PageIdx)
	}

	c := &Connector{client: nil}
	resumed := c.CrawlResume(context.Background(), domain.Source{BaseURL: "https://example.wd1.myworkdayjobs.com"}, cp)
	rIt, ok := resumed.(*iter)
	if !ok {
		t.Fatalf("expected *iter, got %T", resumed)
	}
	if rIt.page != 5 {
		t.Fatalf("resumed iter.page = %d; want 5", rIt.page)
	}
}
