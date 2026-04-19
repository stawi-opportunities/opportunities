package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShardListMatchesLocaleData(t *testing.T) {
	// Shard set in this file must mirror ui/data/locale_shards.yaml
	// (one-time copy — the Go pod doesn't read the Hugo YAML). If the
	// two drift, users land on /l/<CC>/ but the R2 manifest 404s.
	//
	// This test is a loose guard — it doesn't parse the YAML, just
	// verifies the Go list has the minimum published set.
	required := []string{
		"KE", "UG", "TZ", "RW", "ET",
		"NG", "GH",
		"ZA",
		"EG", "MA",
		"US", "GB", "DE",
		"IN", "PH", "BR",
	}
	have := map[string]bool{}
	for _, s := range shardList {
		have[s.Country] = true
	}
	for _, cc := range required {
		if !have[cc] {
			t.Errorf("shardList missing %q — user will 404 on /feeds/%s.json", cc, strings.ToLower(cc))
		}
	}
}

func TestShardListLanguagesNonEmpty(t *testing.T) {
	for _, s := range shardList {
		if len(s.Languages) == 0 {
			t.Errorf("shard %s has no languages — local tier query will skip language filter", s.Country)
		}
	}
}

func TestLowercaseASCII(t *testing.T) {
	cases := map[string]string{
		"KE": "ke",
		"ke": "ke",
		"Us": "us",
		"":   "",
		"G1": "g1", // digits pass through unchanged
	}
	for in, want := range cases {
		if got := lowercaseASCII(in); got != want {
			t.Errorf("lowercaseASCII(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestManifestTier_JSONShape ensures the wire format stays
// wire-compatible with the frontend's FeedTier (see
// ui/app/src/types/search.ts). If this test fails, the frontend
// needs an update in the same commit.
func TestManifestTier_JSONShape(t *testing.T) {
	m := manifestTier{
		ID:       "local",
		Label:    "Jobs in Kenya",
		Cursor:   "",
		HasMore:  false,
		Country:  "KE",
		Language: "en",
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, field := range []string{
		`"id":"local"`,
		`"label":"Jobs in Kenya"`,
		`"has_more":false`,
		`"country":"KE"`,
		`"language":"en"`,
	} {
		if !strings.Contains(s, field) {
			t.Errorf("manifest JSON missing %s: %s", field, s)
		}
	}
}

func TestManifestIndex_JSONShape(t *testing.T) {
	idx := manifestIndex{
		Shards: []indexShardRef{{Country: "KE", Key: "feeds/ke.json"}},
	}
	body, _ := json.Marshal(idx)
	if !strings.Contains(string(body), `"country":"KE"`) ||
		!strings.Contains(string(body), `"key":"feeds/ke.json"`) {
		t.Errorf("index JSON wrong shape: %s", string(body))
	}
}
