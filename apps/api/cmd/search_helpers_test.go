package main

import (
	"encoding/json"
	"strings"
	"testing"
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
