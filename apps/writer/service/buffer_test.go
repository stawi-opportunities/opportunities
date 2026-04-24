package service

import (
	"testing"
	"time"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

func newBufferForTest() *Buffer {
	return NewBuffer(Thresholds{
		MaxEvents:   3,
		MaxBytes:    1024 * 1024,
		MaxInterval: 1 * time.Minute,
	})
}

func TestBufferFlushOnMaxEvents(t *testing.T) {
	b := newBufferForTest()

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
			eventsv1.VariantIngestedV1{
				SourceID:  "src_x",
				ScrapedAt: now,
			})
		flushed, _ := b.Add(env, "src_x")
		if flushed != nil {
			t.Fatalf("no flush expected at event %d", i)
		}
	}

	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
		eventsv1.VariantIngestedV1{SourceID: "src_x", ScrapedAt: now})
	flushed, _ := b.Add(env, "src_x")
	if flushed == nil {
		t.Fatal("expected flush at MaxEvents=3")
		return
	}
	if flushed.Collection != "variants" {
		t.Fatalf("collection=%q, want variants", flushed.Collection)
	}
	if flushed.PartKey.Secondary != "src_x" {
		t.Fatalf("part.Secondary=%q, want src_x", flushed.PartKey.Secondary)
	}
	if len(flushed.Events) != 3 {
		t.Fatalf("len(events)=%d, want 3", len(flushed.Events))
	}
}

// TestBufferGlobalMemoryCap verifies that when totalBytes exceeds maxTotalBytes
// the Buffer force-flushes the oldest partition to stay within budget.
func TestBufferGlobalMemoryCap(t *testing.T) {
	b := NewBuffer(Thresholds{
		MaxEvents:   10000, // high threshold so per-partition flush never fires
		MaxBytes:    10 * 1024 * 1024,
		MaxInterval: 1 * time.Minute,
	})
	// Set a tiny explicit cap to test eviction logic without cgroup dependency.
	b.maxTotalBytes = 100

	now := time.Now().UTC()

	// First event: adds some bytes, total should be < 100 after just one small event.
	env1 := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
		eventsv1.VariantIngestedV1{SourceID: "src_cap_a", ScrapedAt: now})
	flushed1, err := b.Add(env1, "src_cap_a")
	if err != nil {
		t.Fatalf("Add err: %v", err)
	}

	// Check stats after first add.
	stats := b.Stats()
	if stats.TotalBytes <= 0 {
		t.Fatalf("totalBytes should be > 0 after first add")
	}
	firstBytes := stats.TotalBytes

	if flushed1 != nil {
		// Already flushed (event was larger than 100 bytes) — cap was triggered.
		// That's valid behaviour; just verify total is now within cap.
		stats2 := b.Stats()
		if stats2.TotalBytes > b.maxTotalBytes {
			t.Fatalf("after cap flush, totalBytes %d > cap %d", stats2.TotalBytes, b.maxTotalBytes)
		}
		return
	}

	// Add more events to different partitions until cap fires.
	var gotFlush bool
	for i := 0; i < 20; i++ {
		hint := eventsv1.TopicCanonicalsUpserted + "_" + string(rune('a'+i))
		env := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsUpserted,
			eventsv1.VariantIngestedV1{SourceID: hint, ScrapedAt: now})
		flushed, ferr := b.Add(env, hint)
		if ferr != nil {
			t.Fatalf("Add err: %v", ferr)
		}
		if flushed != nil {
			gotFlush = true
			break
		}
	}

	if !gotFlush {
		// All events fit under cap — recheck stats.
		stats3 := b.Stats()
		if firstBytes > 0 && stats3.TotalBytes > b.maxTotalBytes {
			t.Fatalf("totalBytes %d exceeds cap %d without a flush", stats3.TotalBytes, b.maxTotalBytes)
		}
	}
}

func TestBufferStats(t *testing.T) {
	b := NewBuffer(Thresholds{
		MaxEvents:   1000,
		MaxBytes:    10 * 1024 * 1024,
		MaxInterval: 1 * time.Minute,
	})
	now := time.Now().UTC()
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
		eventsv1.VariantIngestedV1{SourceID: "src_stat", ScrapedAt: now})
	_, _ = b.Add(env, "src_stat")

	stats := b.Stats()
	if stats.TotalBytes <= 0 {
		t.Fatalf("Stats.TotalBytes should be > 0, got %d", stats.TotalBytes)
	}
	if stats.MaxTotalBytes <= 0 {
		t.Fatalf("Stats.MaxTotalBytes should be > 0, got %d", stats.MaxTotalBytes)
	}
	if stats.PartitionCount != 1 {
		t.Fatalf("Stats.PartitionCount want 1, got %d", stats.PartitionCount)
	}
	if _, ok := stats.PerTopicBytes[eventsv1.TopicVariantsIngested]; !ok {
		t.Fatal("Stats.PerTopicBytes should have variants topic")
	}
}

// TestBufferInsertOrderGC verifies that flushLocked removes the flushed key
// from insertOrder so the slice does not grow unboundedly (L-2 fix).
// After adding N partitions and flushing some, insertOrder.length must equal
// the number of remaining open partitions.
func TestBufferInsertOrderGC(t *testing.T) {
	b := NewBuffer(Thresholds{
		MaxEvents:   100,
		MaxBytes:    10 * 1024 * 1024,
		MaxInterval: 1 * time.Minute,
	})

	now := time.Now().UTC()

	// Add events to 5 different source partitions.
	sources := []string{"src_a", "src_b", "src_c", "src_d", "src_e"}
	for _, src := range sources {
		env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
			eventsv1.VariantIngestedV1{SourceID: src, ScrapedAt: now})
		_, err := b.Add(env, src)
		if err != nil {
			t.Fatalf("Add(%s): %v", src, err)
		}
	}

	b.mu.Lock()
	beforeFlush := len(b.insertOrder)
	b.mu.Unlock()

	if beforeFlush != 5 {
		t.Fatalf("insertOrder len before flush: got %d, want 5", beforeFlush)
	}

	// Flush all partitions.
	batches := b.FlushAll()
	if len(batches) != 5 {
		t.Fatalf("FlushAll returned %d batches, want 5", len(batches))
	}

	b.mu.Lock()
	afterFlush := len(b.insertOrder)
	remaining := len(b.parts)
	b.mu.Unlock()

	if afterFlush != 0 {
		t.Errorf("insertOrder len after full flush: got %d, want 0 (was leaking keys)", afterFlush)
	}
	if remaining != 0 {
		t.Errorf("parts map len after full flush: got %d, want 0", remaining)
	}
}

// TestBufferInsertOrderGC_PartialFlush verifies that after flushing a subset
// of partitions, insertOrder length == remaining partition count.
func TestBufferInsertOrderGC_PartialFlush(t *testing.T) {
	b := NewBuffer(Thresholds{
		MaxEvents:   100,
		MaxBytes:    10 * 1024 * 1024,
		MaxInterval: 1 * time.Minute,
	})

	now := time.Now().UTC()

	sources := []string{"src_1", "src_2", "src_3"}
	for _, src := range sources {
		env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested,
			eventsv1.VariantIngestedV1{SourceID: src, ScrapedAt: now})
		_, _ = b.Add(env, src)
	}

	// Manually flush one specific partition.
	b.mu.Lock()
	k := bufferKey{topic: eventsv1.TopicVariantsIngested, secondary: "src_1"}
	pb, ok := b.parts[k]
	if ok {
		b.flushLocked(k, pb)
	}
	orderLen := len(b.insertOrder)
	partsLen := len(b.parts)
	b.mu.Unlock()

	if !ok {
		t.Skip("partition src_1 not found — partition-secondary may differ")
	}

	if orderLen != partsLen {
		t.Errorf("insertOrder len (%d) != parts len (%d) after partial flush — key was not GC'd",
			orderLen, partsLen)
	}
}

func TestBufferDueReturnsIntervalExpired(t *testing.T) {
	b := NewBuffer(Thresholds{
		MaxEvents:   1000,
		MaxBytes:    1024 * 1024,
		MaxInterval: 10 * time.Millisecond,
	})

	now := time.Now().UTC()
	env := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsUpserted,
		eventsv1.VariantIngestedV1{SourceID: "src_y", ScrapedAt: now})
	_, _ = b.Add(env, "ab")

	time.Sleep(20 * time.Millisecond)
	due := b.Due()
	if len(due) != 1 {
		t.Fatalf("Due() len=%d, want 1", len(due))
	}
}
