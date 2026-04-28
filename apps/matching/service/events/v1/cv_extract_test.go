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
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// fakeCVExtractor returns a canned CVFields.
type fakeCVExtractor struct{ fields *extraction.CVFields }

func (f *fakeCVExtractor) ExtractCV(_ context.Context, _ string) (*extraction.CVFields, error) {
	if f.fields == nil {
		return nil, errors.New("no fields configured")
	}
	return f.fields, nil
}

// fakeCVScorer returns canned score components.
type fakeCVScorer struct {
	ats, kw, impact, roleFit, clarity, overall int
}

func (f *fakeCVScorer) Score(_ context.Context, _ string, _ *extraction.CVFields, _ string) *ScoreComponents {
	return &ScoreComponents{
		ATS: f.ats, Keywords: f.kw, Impact: f.impact,
		RoleFit: f.roleFit, Clarity: f.clarity, Overall: f.overall,
	}
}

// queuePayloadCollector implements queue.SubscribeWorker by capturing
// raw payloads published on a subject. Tests use one collector per
// subject (improve / embed) since the cv-extract handler fans out to
// both downstream queues.
type queuePayloadCollector struct {
	mu  sync.Mutex
	got [][]byte
}

func (c *queuePayloadCollector) Handle(_ context.Context, _ map[string]string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, append([]byte(nil), payload...))
	return nil
}

func (c *queuePayloadCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

func TestCVExtractHandlerPublishesToImproveAndEmbed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	improveCol := &queuePayloadCollector{}
	embedCol := &queuePayloadCollector{}

	// In-memory queue subjects: one per fan-out target. Frame's
	// mem:// driver is process-local so each subject must round-trip
	// through its own publisher/subscriber pair.
	const improveURL = "mem://cv-improve-test"
	const embedURL = "mem://cv-embed-test"

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-extract-test"),
		frametests.WithNoopDriver(),
		frame.WithRegisterPublisher(eventsv1.SubjectCVImprove, improveURL),
		frame.WithRegisterPublisher(eventsv1.SubjectCVEmbed, embedURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectCVImprove, improveURL, improveCol),
		frame.WithRegisterSubscriber(eventsv1.SubjectCVEmbed, embedURL, embedCol),
	)
	defer svc.Stop(ctx)

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(300 * time.Millisecond)

	h := NewCVExtractHandler(CVExtractDeps{
		Svc: svc,
		Extractor: &fakeCVExtractor{fields: &extraction.CVFields{
			Name:  "Jane Doe",
			Email: "jane@example.com",
		}},
		Scorer:                &fakeCVScorer{ats: 85, overall: 82},
		ExtractorModelVersion: "ext-v1",
		ScorerModelVersion:    "score-v1",
	})

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVUploaded, eventsv1.CVUploadedV1{
		CandidateID:   "cnd_x",
		CVVersion:     1,
		RawArchiveRef: "raw/abc",
		ExtractedText: "resume plain text sufficient length to pass scorer",
	})
	raw, err := json.Marshal(inEnv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := h.Handle(ctx, nil, raw); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if improveCol.Len() == 1 && embedCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if improveCol.Len() != 1 {
		t.Fatalf("improve enqueued=%d, want 1", improveCol.Len())
	}
	if embedCol.Len() != 1 {
		t.Fatalf("embed enqueued=%d, want 1", embedCol.Len())
	}

	// Assert the payload shape on the improve queue.
	improveCol.mu.Lock()
	improvePayload := improveCol.got[0]
	improveCol.mu.Unlock()

	var improveEnv eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(improvePayload, &improveEnv); err != nil {
		t.Fatalf("decode improve payload: %v", err)
	}
	p := improveEnv.Payload
	if p.CandidateID != "cnd_x" || p.Name != "Jane Doe" || p.ScoreOverall != 82 {
		t.Fatalf("bad improve payload: %+v", p)
	}

	// Assert the embed queue carries the same envelope (fan-out
	// duplicates the body so each consumer can decode independently).
	embedCol.mu.Lock()
	embedPayload := embedCol.got[0]
	embedCol.mu.Unlock()
	var embedEnv eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(embedPayload, &embedEnv); err != nil {
		t.Fatalf("decode embed payload: %v", err)
	}
	if embedEnv.Payload.CandidateID != "cnd_x" {
		t.Fatalf("bad embed payload: %+v", embedEnv.Payload)
	}
}

func TestCVExtractHandlerRejectsEmptyPayload(t *testing.T) {
	_, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("cv-extract-empty"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(context.Background())

	h := NewCVExtractHandler(CVExtractDeps{Svc: svc})
	if err := h.Handle(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on empty payload")
	}
}
