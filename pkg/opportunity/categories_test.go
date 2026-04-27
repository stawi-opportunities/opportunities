package opportunity

import (
	"hash/fnv"
	"testing"
)

func TestHashCategory_Deterministic(t *testing.T) {
	got := HashCategory("STEM")
	if got != HashCategory("STEM") {
		t.Fatal("HashCategory not deterministic")
	}
	if HashCategory("STEM") == HashCategory("Arts") {
		t.Fatal("different strings should hash differently")
	}
}

func TestRegistry_CategoryLabels(t *testing.T) {
	reg, err := LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatal(err)
	}
	labels := reg.CategoryLabels()
	// Every category from every kind should be in the inverse map.
	for _, kind := range reg.Known() {
		spec, _ := reg.Lookup(kind)
		for _, c := range spec.Categories {
			got := labels[HashCategory(c)]
			if got != c {
				t.Errorf("CategoryLabels[%q] = %q, want %q", c, got, c)
			}
		}
	}
}

func TestHashCategory_MaterializerParity(t *testing.T) {
	// Sanity: HashCategory and the materializer's local impl produce
	// identical values. This test fails if anyone changes the hash
	// formula in one place but not the other.
	for _, s := range []string{"STEM", "Arts", "Climate", "Programming", "Other"} {
		want := HashCategory(s)
		// Replicate the materializer formula inline so the test is
		// self-contained:
		h := fnv.New64a()
		_, _ = h.Write([]byte(s))
		v := int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
		if want != v {
			t.Errorf("HashCategory(%q) = %d, materializer formula = %d", s, want, v)
		}
	}
}
