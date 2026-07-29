package main

import "testing"

func TestWithSearchBackendDefaultsAndRetiresPgSearch(t *testing.T) {
	p := newJobsPostgres(nil)
	if p.searchBackend != "lakebase_text" {
		t.Fatalf("default backend = %q, want lakebase_text", p.searchBackend)
	}
	p.withSearchBackend("pg_search")
	if p.searchBackend != "lakebase_text" {
		t.Fatalf("pg_search map = %q, want lakebase_text", p.searchBackend)
	}
	p.withSearchBackend("plain")
	if p.searchBackend != "plain" {
		t.Fatalf("plain = %q", p.searchBackend)
	}
	p.withSearchBackend("lakebase")
	if p.searchBackend != "lakebase_text" {
		t.Fatalf("lakebase alias = %q", p.searchBackend)
	}
}
