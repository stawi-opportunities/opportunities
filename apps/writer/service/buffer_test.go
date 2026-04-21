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
