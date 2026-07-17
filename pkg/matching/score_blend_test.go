package matching_test

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestCosineFromPGDistance(t *testing.T) {
	t.Parallel()
	if got := matching.CosineFromPGDistance(0); got != 1 {
		t.Fatalf("distance 0 → 1, got %v", got)
	}
	if got := matching.CosineFromPGDistance(2); got != 0 {
		t.Fatalf("distance 2 → 0, got %v", got)
	}
	mid := matching.CosineFromPGDistance(1)
	if mid < 0.49 || mid > 0.51 {
		t.Fatalf("distance 1 → ~0.5, got %v", mid)
	}
}

func TestBlendFromCosine_MatchesWeightScale(t *testing.T) {
	t.Parallel()
	w := matching.DefaultWeights()
	// Perfect cosine → Cosine*1 + Skills*1 + Geo*1 + Salary*1
	want := w.Cosine + w.Skills + w.Geo + w.Salary
	got := matching.BlendFromCosine(1, w)
	if got != want {
		t.Fatalf("perfect cos: got %v want %v", got, want)
	}
	// Zero cosine still has neutral terms
	got0 := matching.BlendFromCosine(0, w)
	want0 := w.Skills + w.Geo + w.Salary
	if got0 != want0 {
		t.Fatalf("zero cos: got %v want %v", got0, want0)
	}
}
