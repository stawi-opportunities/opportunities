package v1

import "testing"

func TestDigestMinScore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		index, deflt float64
		want         float64
	}{
		{"index wins", 0.62, 0.45, 0.62},
		{"zero index uses default", 0, 0.45, 0.45},
		{"both unset → 0.45 floor", 0, 0, 0.45},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := digestMinScore(tc.index, tc.deflt)
			if got != tc.want {
				t.Fatalf("digestMinScore(%v,%v)=%v want %v", tc.index, tc.deflt, got, tc.want)
			}
		})
	}
}
