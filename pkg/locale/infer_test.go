package locale

import "testing"

func TestInferCountry(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// US patterns — full country name, state names, major cities.
		{"United States", "US"},
		{"USA", "US"},
		{"New York City, NY", "US"},
		{"San Francisco, CA", "US"},
		{"Remote (United States)", "US"},
		// Germany — full, cities, with umlauts.
		{"Germany", "DE"},
		{"Frankfurt am Main", "DE"},
		{"München", "DE"},
		{"Bocholt", "DE"},
		// UK vs Ireland — Dublin should not hijack US "Dublin, OH" (we
		// don't support the US disambiguation; if that becomes common
		// we'll revisit).
		{"London, UK", "GB"},
		{"Edinburgh", "GB"},
		{"Dublin", "IE"},
		// Africa — East + West + North.
		{"Nairobi, Kenya", "KE"},
		{"Kampala", "UG"},
		{"Lagos", "NG"},
		{"Cape Town", "ZA"},
		{"Casablanca, Morocco", "MA"},
		// South-East Asia sample.
		{"Clark, Pampanga", "PH"},
		{"Bangalore", "IN"},
		{"Singapore", "SG"},
		// Latin America.
		{"São Paulo", "BR"},
		{"Buenos Aires, Argentina", "AR"},
		// Unknowable / Remote — must stay empty (we'd rather cascade
		// to global than mislabel a remote listing).
		{"Remote", ""},
		{"Hybrid", ""},
		{"", ""},
		{"   ", ""},
		// Free-text nonsense.
		{"somewhere out there", ""},
	}
	for _, c := range cases {
		got := InferCountry(c.in)
		if got != c.want {
			t.Errorf("InferCountry(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Regression guards: common "substring" false positives.
func TestInferCountry_NoFalsePositives(t *testing.T) {
	// "Indiana" must not match "India" — word boundary guards it.
	if got := InferCountry("Indianapolis, Indiana"); got == "IN" {
		t.Errorf("Indianapolis wrongly inferred as India: %q", got)
	}
	// "Ghana" substring in "Alghanim" shouldn't match.
	if got := InferCountry("Alghanim Industries"); got == "GH" {
		t.Errorf("Alghanim wrongly inferred as Ghana: %q", got)
	}
}
