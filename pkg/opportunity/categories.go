package opportunity

import (
	"hash/fnv"
)

// HashCategory returns the deterministic positive int64 hash of a category.
func HashCategory(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF) // clamp to positive
}

// CategoryLabels returns an int64→string lookup table built from every
// Spec.Categories entry across the registry. The same string in two
// kinds (e.g. "Other") collapses to one entry.
func (r *Registry) CategoryLabels() map[int64]string {
	out := map[int64]string{}
	for _, s := range r.specs {
		for _, c := range s.Categories {
			out[HashCategory(c)] = c
		}
	}
	return out
}
