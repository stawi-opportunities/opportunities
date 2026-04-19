package locale

import (
	"reflect"
	"testing"
)

func TestRegionOf(t *testing.T) {
	cases := map[string]Region{
		"KE":   RegionEastAfrica,
		"ke":   RegionEastAfrica,
		" UG ": RegionEastAfrica,
		"NG":   RegionWestAfrica,
		"ZA":   RegionSouthernAfrica,
		"EG":   RegionNorthAfrica,
		"CM":   RegionCentralAfrica,
		"US":   RegionNorthAmerica,
		"BR":   RegionLatinAmerica,
		"GB":   RegionWesternEurope,
		"SE":   RegionNorthernEurope,
		"AU":   RegionOceania,
		"IN":   RegionSouthAsia,
		"":     RegionUnknown,
		"ZZ":   RegionUnknown,
	}
	for cc, want := range cases {
		if got := RegionOf(cc); got != want {
			t.Errorf("RegionOf(%q) = %s, want %s", cc, got, want)
		}
	}
}

func TestCountriesIn_EastAfrica(t *testing.T) {
	got := CountriesIn(RegionEastAfrica)
	want := []string{"BI", "DJ", "ER", "ET", "KE", "RW", "SO", "SS", "TZ", "UG"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CountriesIn(east_africa) = %v, want %v", got, want)
	}
}

func TestCountriesIn_ReturnsFreshSlice(t *testing.T) {
	// Defensive: caller should be able to mutate the returned slice
	// without affecting subsequent calls.
	got := CountriesIn(RegionEastAfrica)
	got[0] = "XX"
	got2 := CountriesIn(RegionEastAfrica)
	if got2[0] == "XX" {
		t.Error("CountriesIn must return a fresh copy each call")
	}
}

func TestRegionLabel(t *testing.T) {
	if RegionLabel(RegionEastAfrica) != "East Africa" {
		t.Error("east africa label wrong")
	}
	if RegionLabel(RegionUnknown) != "Worldwide" {
		t.Errorf("unknown → %s, want Worldwide", RegionLabel(RegionUnknown))
	}
}

func TestBaseTag(t *testing.T) {
	cases := map[string]string{
		"en":      "en",
		"en-US":   "en",
		"sw-KE":   "sw",
		"zh-Hans": "zh",
		"pt_BR":   "pt",
		"EN-us":   "en",
		" fr-CA ": "fr",
		"":        "",
		"xx":      "xx", // unrecognised subtag — accept verbatim
	}
	for in, want := range cases {
		if got := BaseTag(in); got != want {
			t.Errorf("BaseTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLanguageMatches(t *testing.T) {
	if !LanguageMatches("en-US", "en") {
		t.Error("en-US should match en")
	}
	if !LanguageMatches("sw-KE", "sw") {
		t.Error("sw-KE should match sw")
	}
	if LanguageMatches("en", "fr") {
		t.Error("en vs fr must not match")
	}
	if LanguageMatches("", "en") {
		t.Error("empty job tag must not match")
	}
	if LanguageMatches("en", "") {
		t.Error("empty user tag must not match")
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"en", []string{"en"}},
		{"en-US", []string{"en"}},
		{"en-US,en;q=0.9,sw-KE;q=0.8,fr;q=0.5", []string{"en", "sw", "fr"}},
		// q-weight ordering honoured; ties break on position.
		{"fr;q=0.3,en;q=0.9,sw;q=0.5", []string{"en", "sw", "fr"}},
		// Duplicate base tags merged, higher q wins.
		{"en-GB;q=0.5,en-US;q=0.9", []string{"en"}},
		// Clamp to 5 at most.
		{"en,fr,sw,es,pt,de,nl,it", []string{"en", "fr", "sw", "es", "pt"}},
	}
	for _, c := range cases {
		got := ParseAcceptLanguage(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseAcceptLanguage(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
