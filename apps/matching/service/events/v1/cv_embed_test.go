package v1

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// fakeEmbedder returns a canned vector.
type fakeEmbedder struct{ vec []float32; err error }

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return f.vec, f.err
}

type embedCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
}

func (c *embedCollector) Name() string     { return eventsv1.TopicCandidateEmbedding }
func (c *embedCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *embedCollector) Validate(context.Context, any) error { return nil }
func (c *embedCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *embedCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVEmbedHandlerEmitsEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-embed-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &embedCollector{}
	svc.EventsManager().Add(col)

	h := NewCVEmbedHandler(CVEmbedDeps{
		Svc:          svc,
		Embedder:     &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}},
		ModelVersion: "embed-v1",
	})

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{
		CandidateID: "cnd_1", CVVersion: 1, Bio: "seasoned backend engineer in Nairobi",
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
	if p.CandidateID != "cnd_1" || len(p.Vector) != 3 {
		t.Fatalf("bad payload: %+v", p)
	}
}

func TestCVEmbedHandlerReturnsErrorOnEmbedFailure(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("cv-embed-err"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	h := NewCVEmbedHandler(CVEmbedDeps{
		Svc:      svc,
		Embedder: &fakeEmbedder{err: errors.New("backend down")},
	})

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{
		CandidateID: "cnd_x", CVVersion: 1, Bio: "experienced developer",
	})
	raw, _ := json.Marshal(inEnv)
	rm := json.RawMessage(raw)
	err := h.Execute(ctx, &rm)
	if err == nil {
		t.Fatalf("expected error when embedder fails, got nil (Frame redelivery required)")
	}
}
