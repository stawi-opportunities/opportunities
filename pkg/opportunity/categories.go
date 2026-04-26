package opportunity

import (
	"hash/fnv"
)

// HashCategory returns the deterministic int64 hash of a category string,
// matching the materializer's encoding into Manticore's multi64 column.
// Identical formula to apps/materializer/service/indexer.go::hashCategory —
// the two MUST produce the same value for the same input.
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
