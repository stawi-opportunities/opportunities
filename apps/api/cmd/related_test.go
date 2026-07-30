package main

import "testing"

func TestRelatedQueryFromTitle(t *testing.T) {
	q := relatedQueryFromTitle("Senior Backend Engineer (Remote) — Go / PostgreSQL", "Acme")
	if q == "" {
		t.Fatal("expected non-empty query")
	}
	if !containsAll(q, "backend", "engineer") {
		t.Fatalf("query %q missing role tokens", q)
	}
	// stop words stripped
	if containsToken(q, "senior") || containsToken(q, "remote") {
		t.Fatalf("query %q should drop stop/seniority tokens", q)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !containsToken(s, p) {
			return false
		}
	}
	return true
}

func containsToken(s, tok string) bool {
	for _, f := range splitWS(s) {
		if f == tok {
			return true
		}
	}
	return false
}

func splitWS(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
