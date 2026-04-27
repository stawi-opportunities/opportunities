package icebergclient

import (
	"strings"
	"testing"
)

// TestSchemasMatchTables guards against drift between AppendOnlyTables
// (used by maintenance jobs) and AllTableSchemas (used by bootstrap).
// Adding a table requires updating both lists.
func TestSchemasMatchTables(t *testing.T) {
	t.Parallel()

	if len(AppendOnlyTables) != len(AllTableSchemas()) {
		t.Fatalf("AppendOnlyTables (%d) and AllTableSchemas (%d) length mismatch — keep them in sync",
			len(AppendOnlyTables), len(AllTableSchemas()))
	}

	for i, want := range AppendOnlyTables {
		got := AllTableSchemas()[i].Identifier
		if len(want) != len(got) {
			t.Fatalf("table %d: identifier length differs — want %v, got %v", i, want, got)
		}
		for j := range want {
			if want[j] != got[j] {
				t.Fatalf("table %d part %d mismatch: want %q got %q", i, j, want[j], got[j])
			}
		}
	}
}

// TestAllSchemasConstruct verifies every schema constructor returns a
// schema with the expected number of columns (mirrors the column-count
// assertions in definitions/iceberg/_schemas.py).
func TestAllSchemasConstruct(t *testing.T) {
	t.Parallel()

	type expectation struct {
		ident         string
		expectFields  int
		mustHaveCol   string
		mustHaveExtra []string
	}
	want := map[string]expectation{
		"opportunities/variants":             {ident: "opportunities/variants", expectFields: 22, mustHaveCol: "kind"},
		"opportunities/variants_rejected":    {ident: "opportunities/variants_rejected", expectFields: 6, mustHaveCol: "rejected_at"},
		"opportunities/embeddings":           {ident: "opportunities/embeddings", expectFields: 4, mustHaveCol: "vector"},
		"opportunities/published":            {ident: "opportunities/published", expectFields: 22, mustHaveCol: "stage"},
		"opportunities/crawl_page_completed": {ident: "opportunities/crawl_page_completed", expectFields: 12, mustHaveCol: "request_id", mustHaveExtra: []string{"event_id", "occurred_at"}},
		"opportunities/sources_discovered":   {ident: "opportunities/sources_discovered", expectFields: 7, mustHaveCol: "discovered_url", mustHaveExtra: []string{"event_id", "occurred_at"}},
		"candidates/cv_uploaded":             {ident: "candidates/cv_uploaded", expectFields: 9, mustHaveCol: "raw_archive_ref", mustHaveExtra: []string{"event_id", "occurred_at"}},
		"candidates/cv_extracted":            {ident: "candidates/cv_extracted", expectFields: 33, mustHaveCol: "score_overall", mustHaveExtra: []string{"event_id", "occurred_at"}},
		"candidates/cv_improved":             {ident: "candidates/cv_improved", expectFields: 6, mustHaveCol: "fixes", mustHaveExtra: []string{"event_id", "occurred_at"}},
		"candidates/preferences":             {ident: "candidates/preferences", expectFields: 5, mustHaveCol: "opt_ins_json", mustHaveExtra: []string{"event_id", "occurred_at"}},
		"candidates/embeddings":              {ident: "candidates/embeddings", expectFields: 6, mustHaveCol: "vector", mustHaveExtra: []string{"event_id", "occurred_at"}},
		"candidates/matches_ready":           {ident: "candidates/matches_ready", expectFields: 5, mustHaveCol: "matches", mustHaveExtra: []string{"event_id", "occurred_at"}},
	}

	for _, ts := range AllTableSchemas() {
		key := strings.Join(ts.Identifier, "/")
		exp, ok := want[key]
		if !ok {
			t.Errorf("unexpected table %q — update want map", key)
			continue
		}
		s := ts.Schema()
		if got := len(s.Fields()); got != exp.expectFields {
			t.Errorf("%s: field count %d, want %d", key, got, exp.expectFields)
		}
		if _, ok := s.FindFieldByName(exp.mustHaveCol); !ok {
			t.Errorf("%s: missing expected column %q", key, exp.mustHaveCol)
		}
		for _, c := range exp.mustHaveExtra {
			if _, ok := s.FindFieldByName(c); !ok {
				t.Errorf("%s: missing expected envelope column %q", key, c)
			}
		}
	}
}

// TestBootstrapNamespacesUnique ensures BootstrapNamespaces returns the
// distinct top-level namespaces used by AllTableSchemas, in insertion
// order, with no duplicates.
func TestBootstrapNamespacesUnique(t *testing.T) {
	t.Parallel()

	got := BootstrapNamespaces()
	if len(got) != 2 {
		t.Fatalf("expected 2 namespaces (opportunities + candidates), got %d: %v", len(got), got)
	}
	if got[0][0] != "opportunities" || got[1][0] != "candidates" {
		t.Fatalf("namespace order wrong: %v", got)
	}
}
