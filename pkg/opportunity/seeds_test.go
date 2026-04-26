package opportunity

import "testing"

func TestSeedKindsLoad(t *testing.T) {
	reg, err := LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	want := []string{"deal", "funding", "job", "scholarship", "tender"}
	got := reg.Known()
	if len(got) != len(want) {
		t.Fatalf("Known() = %v, want %v", got, want)
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("Known()[%d] = %q, want %q", i, got[i], k)
		}
	}
	for _, k := range want {
		s, _ := reg.Lookup(k)
		if s.URLPrefix == "" || s.OnboardingFlow == "" || s.Matcher == "" {
			t.Errorf("kind %q: incomplete spec %+v", k, s)
		}
	}
}
