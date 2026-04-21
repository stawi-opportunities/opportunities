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

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/extraction"
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

type extractedCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CVExtractedV1]
}

func (c *extractedCollector) Name() string     { return eventsv1.TopicCVExtracted }
func (c *extractedCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *extractedCollector) Validate(context.Context, any) error { return nil }
func (c *extractedCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *extractedCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVExtractHandlerEmitsExtracted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-extract-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &extractedCollector{}
	svc.EventsManager().Add(col)

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

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	inEnv := eventsv1.NewEnvelope(eventsv1.TopicCVUploaded, eventsv1.CVUploadedV1{
		CandidateID:   "cnd_x",
		CVVersion:     1,
		RawArchiveRef: "raw/abc",
		ExtractedText: "resume plain text sufficient length to pass scorer",
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
	if p.CandidateID != "cnd_x" || p.Name != "Jane Doe" || p.ScoreOverall != 82 {
		t.Fatalf("bad payload: %+v", p)
	}
}
