package candidatestore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	t0 = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = t0.Add(24 * time.Hour)
	t2 = t0.Add(48 * time.Hour)
	t3 = t0.Add(72 * time.Hour)
)

// TestStaleHeap_Retract verifies that retract removes a candidateID from the heap.
func TestStaleHeap_Retract(t *testing.T) {
	h := newStaleHeap(10)
	h.offer(StaleCandidate{CandidateID: "c1", LastUploadAt: t0})
	h.offer(StaleCandidate{CandidateID: "c2", LastUploadAt: t1})
	h.offer(StaleCandidate{CandidateID: "c3", LastUploadAt: t2})

	assert.Equal(t, 3, h.Len())
	h.retract("c2")
	assert.Equal(t, 2, h.Len())

	// c2 should not appear in sorted output.
	out := h.sorted()
	for _, c := range out {
		assert.NotEqual(t, "c2", c.CandidateID, "c2 should have been retracted")
	}
}

// TestStaleHeap_Retract_Nonexistent verifies retract of missing ID is a no-op.
func TestStaleHeap_Retract_Nonexistent(t *testing.T) {
	h := newStaleHeap(10)
	h.offer(StaleCandidate{CandidateID: "c1", LastUploadAt: t0})
	h.retract("nonexistent") // should not panic
	assert.Equal(t, 1, h.Len())
}

// TestStaleHeap_OfferOrRetract_Update verifies that offerOrRetract updates
// an existing entry when the new ts is more recent, and the updated ts is
// still before the cutoff (so the entry remains stale).
func TestStaleHeap_OfferOrRetract_Update(t *testing.T) {
	h := newStaleHeap(10)
	h.offer(StaleCandidate{CandidateID: "c1", LastUploadAt: t0})

	// Use a cutoff far in the future so the updated ts (t2) remains stale.
	farFuture := t3.Add(365 * 24 * time.Hour)

	// Update with newer ts — still before cutoff, entry should remain.
	h.offerOrRetract(StaleCandidate{CandidateID: "c1", LastUploadAt: t2}, farFuture)
	assert.Equal(t, 1, h.Len())

	out := h.sorted()
	assert.Equal(t, t2, out[0].LastUploadAt, "entry should have updated LastUploadAt")
}

// TestStaleHeap_OfferOrRetract_OlderIgnored verifies that offerOrRetract
// does not downgrade an entry when the new ts is older.
func TestStaleHeap_OfferOrRetract_OlderIgnored(t *testing.T) {
	h := newStaleHeap(10)
	h.offer(StaleCandidate{CandidateID: "c1", LastUploadAt: t2})

	farFuture := t3.Add(365 * 24 * time.Hour)

	// Attempt to update with older ts — should be ignored.
	h.offerOrRetract(StaleCandidate{CandidateID: "c1", LastUploadAt: t0}, farFuture)
	assert.Equal(t, 1, h.Len())

	out := h.sorted()
	assert.Equal(t, t2, out[0].LastUploadAt, "entry should keep newer LastUploadAt")
}

// TestStaleHeap_OfferOrRetract_BecomesNonStale verifies that when offerOrRetract
// sees an updated timestamp that is >= cutoff, the entry is removed from the heap.
func TestStaleHeap_OfferOrRetract_BecomesNonStale(t *testing.T) {
	cutoff := t2.Add(time.Hour) // t2+1h; t3 is after this cutoff

	h := newStaleHeap(10)
	// Initially stale: t0 < cutoff.
	h.offer(StaleCandidate{CandidateID: "c1", LastUploadAt: t0})
	assert.Equal(t, 1, h.Len())

	// Update with a timestamp that is >= cutoff → should be removed.
	h.offerOrRetract(StaleCandidate{CandidateID: "c1", LastUploadAt: t3}, cutoff)
	assert.Equal(t, 0, h.Len(), "entry should have been removed once ts >= cutoff")
}

// TestFlushStaleMap_RetractsNonStale verifies that flushStaleMap removes
// a previously-stale entry when a non-stale ts is seen in the same flush.
func TestFlushStaleMap_RetractsNonStale(t *testing.T) {
	cutoff := t1.Add(time.Hour) // anything before cutoff is stale

	h := newStaleHeap(10)
	// Pre-populate heap with a stale entry for "cand-x".
	h.offer(StaleCandidate{CandidateID: "cand-x", LastUploadAt: t0}) // t0 < cutoff → stale

	assert.Equal(t, 1, h.Len())

	// Now flush a map where "cand-x" has a newer, non-stale ts.
	m := map[string]time.Time{
		"cand-x": t3, // t3 > cutoff → non-stale
	}
	flushStaleMap(m, cutoff, h)

	// "cand-x" should have been retracted.
	assert.Equal(t, 0, h.Len(), "cand-x should be retracted after non-stale flush")
}

// TestFlushStaleMap_AddsStale verifies that flushStaleMap adds stale candidates.
func TestFlushStaleMap_AddsStale(t *testing.T) {
	cutoff := t2.Add(time.Hour) // t0 and t1 are stale; t3 is not

	h := newStaleHeap(10)
	m := map[string]time.Time{
		"cand-a": t0, // stale
		"cand-b": t1, // stale
		"cand-c": t3, // not stale
	}
	flushStaleMap(m, cutoff, h)

	assert.Equal(t, 2, h.Len(), "only stale candidates should be in heap")
	out := h.sorted()
	ids := make(map[string]bool)
	for _, c := range out {
		ids[c.CandidateID] = true
	}
	assert.True(t, ids["cand-a"])
	assert.True(t, ids["cand-b"])
	assert.False(t, ids["cand-c"])
}

// TestBoundedFlushCorrectness verifies the full bounded-flush cycle:
// initial stale emit → later non-stale overwrite → retracted from heap.
func TestBoundedFlushCorrectness(t *testing.T) {
	cutoff := t1.Add(time.Hour)
	h := newStaleHeap(10)

	// Batch 1: "cand-z" has an old upload → goes into heap as stale.
	flushStaleMap(map[string]time.Time{"cand-z": t0}, cutoff, h)
	assert.Equal(t, 1, h.Len())

	// Batch 2: "cand-z" uploads again with a newer, non-stale ts.
	flushStaleMap(map[string]time.Time{"cand-z": t3}, cutoff, h)
	assert.Equal(t, 0, h.Len(), "cand-z should be retracted after non-stale re-upload")
}
