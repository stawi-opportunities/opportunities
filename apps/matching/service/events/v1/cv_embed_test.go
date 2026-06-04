package v1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
)

// fakeEmbedder returns a canned vector.
type fakeEmbedder struct {
	vec []float32
	err error
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return f.vec, f.err
}

func TestCVEmbedHandlerEmitsEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// cv-embed publishes CandidateEmbeddingV1 onto a dedicated durable
	// queue (not the events bus). Drain it with a queuePayloadCollector
	// over an in-memory subject — mirrors the live wiring in cmd/main.go.
	const queueName = eventsv1.TopicCandidateEmbedding
	const queueURL = "mem://candidate-embedding-test"

	col := &queuePayloadCollector{}
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-embed-test"),
		frametests.WithNoopDriver(),
		frame.WithRegisterPublisher(queueName, queueURL),
		frame.WithRegisterSubscriber(queueName, queueURL, col),
	)
	defer svc.Stop(ctx)

	h := NewCVEmbedHandler(CVEmbedDeps{
		Svc:                         svc,
		Embedder:                    &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}},
		ModelVersion:                "embed-v1",
		CandidateEmbeddingQueueName: queueName,
	})

	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, queueName, 2*time.Second)

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, eventsv1.CVExtractedV1{
		CandidateID: "cnd_1", CVVersion: 1, Bio: "seasoned backend engineer in Nairobi",
	})
	raw, err := json.Marshal(inEnv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := h.Handle(ctx, nil, raw); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("published=%d, want 1", col.Len())
	}

	col.mu.Lock()
	payload := col.got[0]
	col.mu.Unlock()
	var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("decode embed payload: %v", err)
	}
	p := env.Payload
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
	raw, err := json.Marshal(inEnv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := h.Handle(ctx, nil, raw); err == nil {
		t.Fatalf("expected error when embedder fails, got nil (Frame redelivery required)")
	}
}
