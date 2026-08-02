package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOpportunityAPIShapesAlwaysExposeApplyURL(t *testing.T) {
	const applyURL = "https://example.test/apply/role"
	j := job{Slug: "role", Title: "Role", ApplyURL: applyURL}

	search := toSearchResult(j, nil)
	if search.ApplyURL != applyURL {
		t.Fatalf("search apply_url = %q", search.ApplyURL)
	}

	detailJSON, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(detailJSON), `"apply_url":"`+applyURL+`"`) {
		t.Fatalf("detail response omits apply_url: %s", detailJSON)
	}
}

func TestToSearchResultIncludesDeadline(t *testing.T) {
	dl := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	j := job{Slug: "s", Title: "T", ApplyURL: "https://x.test", Deadline: &dl, Kind: "scholarship"}
	search := toSearchResult(j, nil)
	if search.Deadline == nil || !search.Deadline.Equal(dl) {
		t.Fatalf("deadline = %v, want %v", search.Deadline, dl)
	}
	raw, err := json.Marshal(search)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"deadline"`) {
		t.Fatalf("search result JSON missing deadline: %s", raw)
	}
}

func TestOrderByClauseClosingSoon(t *testing.T) {
	got := orderByClause("closing_soon")
	if !strings.Contains(got, "deadline ASC") {
		t.Fatalf("closing_soon order = %q, want deadline ASC", got)
	}
	got = orderByClause("recent")
	if !strings.Contains(got, "posted_at DESC") {
		t.Fatalf("recent order = %q, want posted_at DESC", got)
	}
}
