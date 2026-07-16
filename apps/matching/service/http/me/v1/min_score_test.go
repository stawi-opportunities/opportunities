package v1

import "testing"

func TestEffectiveMinScore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		index, deflt float64
		want         float64
	}{
		{"index wins", 0.7, 0.45, 0.7},
		{"zero index uses default", 0, 0.45, 0.45},
		{"negative index uses default", -1, 0.5, 0.5},
		{"both zero → platform floor", 0, 0, 0.45},
		{"default above 1 ignored", 0, 1.5, 0.45},
		{"index above 1 ignored", 1.2, 0.4, 0.4},
		{"exact 1 ok", 1, 0.45, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := effectiveMinScore(tc.index, tc.deflt)
			if got != tc.want {
				t.Fatalf("effectiveMinScore(%v,%v)=%v want %v", tc.index, tc.deflt, got, tc.want)
			}
		})
	}
}
