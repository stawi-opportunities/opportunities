package v1

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// fakeFixGenerator returns a hard-coded slice of PriorityFix.
type fakeFixGenerator struct{ fixes []PriorityFix }

func (f *fakeFixGenerator) Generate(_ context.Context, _ *eventsv1.CVExtractedV1) ([]PriorityFix, error) {
	return f.fixes, nil
}

type improvedCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CVImprovedV1]
}

func (c *improvedCollector) Name() string     { return eventsv1.TopicCVImproved }
func (c *improvedCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *improvedCollector) Validate(context.Context, any) error { return nil }
func (c *improvedCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVImprovedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *improvedCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVImproveHandlerEmitsImproved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-improve-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &improvedCollector{}
	svc.EventsManager().Add(col)

	h := NewCVImproveHandler(CVImproveDeps{
		Svc: svc,
		Fixes: &fakeFixGenerator{fixes: []PriorityFix{
			{FixID: "fix-1", Title: "Add metrics", ImpactLevel: "high", Category: "impact", Why: "bullets lack numbers", AutoApplicable: true, Rewrite: "Reduced latency 40%"},
		}},
		ModelVersion: "improve-v1",
	})

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{
		CandidateID: "cnd_1", CVVersion: 1, ScoreOverall: 70,
	})
	raw, _ := json.Marshal(inEnv)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_1" || len(p.Fixes) != 1 || p.Fixes[0].FixID != "fix-1" {
		t.Fatalf("bad payload: %+v", p)
	}
}
