package translate

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"trim and lower", []string{" EN ", "Fr", "DE"}, []string{"en", "fr", "de"}},
		{"dedupe", []string{"en", "EN", "en"}, []string{"en"}},
		{"drop empties", []string{"en", "", " ", "es"}, []string{"en", "es"}},
		{"nil input", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Normalize(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLanguageName_known(t *testing.T) {
	if n := languageName("fr"); n != "French" {
		t.Errorf("languageName(fr) = %q, want French", n)
	}
	if n := languageName("zh"); n != "Chinese (Simplified)" {
		t.Errorf("languageName(zh) = %q", n)
	}
}

func TestLanguageName_unknown(t *testing.T) {
	if n := languageName("xx"); n != "xx" {
		t.Errorf("languageName(xx) = %q, want fall-through", n)
	}
}

func TestDefaultLanguages_hasAllEight(t *testing.T) {
	want := map[string]struct{}{"en": {}, "es": {}, "fr": {}, "de": {}, "pt": {}, "ja": {}, "ar": {}, "zh": {}}
	if len(DefaultLanguages) != len(want) {
		t.Fatalf("DefaultLanguages has %d entries, want %d", len(DefaultLanguages), len(want))
	}
	for _, c := range DefaultLanguages {
		if _, ok := want[c]; !ok {
			t.Errorf("DefaultLanguages contains unexpected code %q", c)
		}
	}
}
