package main

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"stawi.jobs/pkg/locale"
)

func TestInferContextFromRequest_CFHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/feed", nil)
	r.Header.Set("CF-IPCountry", "KE")
	r.Header.Set("Accept-Language", "en-US,en;q=0.9,sw-KE;q=0.8")
	country, langs := inferContextFromRequest(r)
	if country != "KE" {
		t.Errorf("country=%q want KE", country)
	}
	if !reflect.DeepEqual(langs, []string{"en", "sw"}) {
		t.Errorf("langs=%v want [en sw]", langs)
	}
}

func TestInferContextFromRequest_QueryOverride(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/feed?country=ng&lang=fr,en", nil)
	r.Header.Set("CF-IPCountry", "KE") // overridden by query
	country, langs := inferContextFromRequest(r)
	if country != "NG" {
		t.Errorf("country=%q want NG", country)
	}
	if !reflect.DeepEqual(langs, []string{"fr", "en"}) {
		t.Errorf("langs=%v want [fr en]", langs)
	}
}

func TestInferContextFromRequest_Empty(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/feed", nil)
	country, langs := inferContextFromRequest(r)
	if country != "" {
		t.Errorf("country=%q want empty", country)
	}
	if len(langs) != 0 {
		t.Errorf("langs=%v want empty", langs)
	}
}

func TestParseBoostList(t *testing.T) {
	if got := parseBoostList("us,de, gb ", "country"); !reflect.DeepEqual(got, []string{"US", "DE", "GB"}) {
		t.Errorf("country list: got %v", got)
	}
	if got := parseBoostList("en-US,sw-KE", "language"); !reflect.DeepEqual(got, []string{"en", "sw"}) {
		t.Errorf("lang list: got %v", got)
	}
	if got := parseBoostList("", "country"); got != nil {
		t.Errorf("empty: got %v, want nil", got)
	}
}

func TestFilterOut(t *testing.T) {
	got := filterOut([]string{"KE", "UG", "TZ", "RW"}, []string{"UG", "TZ"})
	want := []string{"KE", "RW"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	xs := []string{"KE", "NG"}
	if !containsIgnoreCase(xs, "ke") {
		t.Error("case-insensitive match failed")
	}
	if containsIgnoreCase(xs, "ZA") {
		t.Error("false positive")
	}
}

func TestCountryDisplayName(t *testing.T) {
	cases := map[string]string{
		"KE": "Kenya",
		"us": "the United States",
		"ZW": "ZW", // unmapped → echoed
		"":   "your region",
	}
	for in, want := range cases {
		if got := countryDisplayName(in); got != want {
			t.Errorf("countryDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFeedHandler_NoDeps_ReturnsContext isn't useful without a repo,
// but this compile-check confirms inferContextFromRequest keeps its
// contract with locale.RegionOf (so a missing region doesn't crash).
func TestInferContext_RegionSafe(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/feed?country=ZZ", nil)
	country, _ := inferContextFromRequest(r)
	if locale.RegionOf(country) != locale.RegionUnknown {
		t.Errorf("unmapped country should yield RegionUnknown, got %s", locale.RegionOf(country))
	}
}
