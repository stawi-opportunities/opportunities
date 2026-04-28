package service

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"

	adminv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/admin/v1"
	eventv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/events/v1"
	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
)

// --- fakes (local to the e2e test) ---

type fakeText struct{ text string }

func (f *fakeText) FromPDF(_ []byte) (string, error)  { return f.text, nil }
func (f *fakeText) FromDOCX(_ []byte) (string, error) { return f.text, nil }

type fakeExtractor struct{ fields *extraction.CVFields }

func (f *fakeExtractor) ExtractCV(_ context.Context, _ string) (*extraction.CVFields, error) {
	return f.fields, nil
}

type fakeScorer struct{}

func (f *fakeScorer) Score(_ context.Context, _ string, _ *extraction.CVFields, _ string) *eventv1.ScoreComponents {
	return &eventv1.ScoreComponents{ATS: 85, Keywords: 80, Impact: 78, RoleFit: 82, Clarity: 88, Overall: 82}
}

type fakeFixes struct{}

func (f *fakeFixes) Generate(_ context.Context, _ *eventsv1.CVExtractedV1) ([]eventv1.PriorityFix, error) {
	return []eventv1.PriorityFix{{FixID: "fix-1", Title: "Add metrics", AutoApplicable: true}}, nil
}

type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.11, 0.22, 0.33}, nil
}

// --- generic event collector (still useful for the terminal events
// that cv-improve / cv-embed emit on the events bus). ---

type envCol[P any] struct {
	topic string
	mu    sync.Mutex
	got   []eventsv1.Envelope[P]
}

func (c *envCol[P]) Name() string                          { return c.topic }
func (c *envCol[P]) PayloadType() any                      { var raw json.RawMessage; return &raw }
func (c *envCol[P]) Validate(context.Context, any) error   { return nil }
func (c *envCol[P]) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[P]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *envCol[P]) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

// --- test ---

// TestCandidatesE2EUploadToEmbedding drives the full upload → cv-extract
// → cv-improve / cv-embed pipeline through Frame's in-memory queue
// (mem://) so we exercise the production transport without NATS. The
// cv-extract handler fans out to two queue subjects; cv-improve emits
// CVImprovedV1 onto the events bus and cv-embed emits
// CandidateEmbeddingV1.
func TestCandidatesE2EUploadToEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const extractURL = "mem://e2e.cv.extract"
	const improveURL = "mem://e2e.cv.improve"
	const embedURL = "mem://e2e.cv.embed"

	// Production handlers — constructed with svc (set after init).
	var extractH *eventv1.CVExtractHandler
	var improveH *eventv1.CVImproveHandler
	var embedH *eventv1.CVEmbedHandler
	// Production handler factories take *frame.Service so we wire up
	// in two stages: build the service with publishers + placeholder
	// subscribers (the handlers reference svc), then call Init for the
	// subscribers. Frame's option pipeline allows constructing handlers
	// that hold a back-pointer to svc as long as we register them
	// inside the same NewServiceWithContext call as svc itself.
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("candidates-e2e"),
		frametests.WithNoopDriver(),
		frame.WithRegisterPublisher(eventsv1.SubjectCVExtract, extractURL),
		frame.WithRegisterPublisher(eventsv1.SubjectCVImprove, improveURL),
		frame.WithRegisterPublisher(eventsv1.SubjectCVEmbed, embedURL),
	)
	defer svc.Stop(ctx)

	extractH = eventv1.NewCVExtractHandler(eventv1.CVExtractDeps{
		Svc:       svc,
		Extractor: &fakeExtractor{fields: &extraction.CVFields{Name: "Jane", Bio: "backend engineer"}},
		Scorer:    &fakeScorer{},
	})
	improveH = eventv1.NewCVImproveHandler(eventv1.CVImproveDeps{
		Svc: svc, Fixes: &fakeFixes{},
	})
	embedH = eventv1.NewCVEmbedHandler(eventv1.CVEmbedDeps{
		Svc: svc, Embedder: &fakeEmbedder{},
	})

	// Register subscribers post-construction.
	svc.Init(ctx,
		frame.WithRegisterSubscriber(eventsv1.SubjectCVExtract, extractURL, extractH),
		frame.WithRegisterSubscriber(eventsv1.SubjectCVImprove, improveURL, improveH),
		frame.WithRegisterSubscriber(eventsv1.SubjectCVEmbed, embedURL, embedH),
	)

	// Terminal-event collectors — cv-improve emits CVImprovedV1, cv-embed
	// emits CandidateEmbeddingV1, and the preferences flow emits
	// PreferencesUpdatedV1. These all stay on the events bus because
	// they are fast/internal — only the external-LLM stages were
	// migrated to Queue.
	improvedCol := &envCol[eventsv1.CVImprovedV1]{topic: eventsv1.TopicCVImproved}
	embeddingCol := &envCol[eventsv1.CandidateEmbeddingV1]{topic: eventsv1.TopicCandidateEmbedding}
	prefsCol := &envCol[eventsv1.PreferencesUpdatedV1]{topic: eventsv1.TopicCandidatePreferencesUpdated}
	svc.EventsManager().Add(improvedCol)
	svc.EventsManager().Add(embeddingCol)
	svc.EventsManager().Add(prefsCol)

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(400 * time.Millisecond)

	// --- POST /candidates/cv/upload ---
	uploadHandler := httpv1.UploadHandler(httpv1.UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeText{text: "resume plain text long enough to be usable"},
	})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("candidate_id", "cnd_e2e")
	fw, _ := mw.CreateFormFile("cv", "resume.pdf")
	_, _ = fw.Write([]byte("%PDF-1.4 fake"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/candidates/cv/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	uploadHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if improvedCol.Len() >= 1 && embeddingCol.Len() >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if improvedCol.Len() != 1 {
		t.Fatalf("improved=%d, want 1", improvedCol.Len())
	}
	if embeddingCol.Len() != 1 {
		t.Fatalf("embedding=%d, want 1", embeddingCol.Len())
	}

	// --- POST /candidates/preferences ---
	prefsHandler := httpv1.PreferencesHandler(svc)
	jobBlob, _ := json.Marshal(map[string]any{
		"target_roles": []string{"backend-engineer"},
		"salary_min":   70000,
		"locations":    map[string]any{"remote_ok": true},
	})
	body := map[string]any{
		"candidate_id": "cnd_e2e",
		"opt_ins":      map[string]json.RawMessage{"job": jobBlob},
	}
	raw, _ := json.Marshal(body)
	req = httptest.NewRequest(http.MethodPost, "/candidates/preferences", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	prefsHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("prefs status=%d body=%s", rec.Code, rec.Body.String())
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if prefsCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if prefsCol.Len() != 1 {
		t.Fatalf("prefs=%d, want 1", prefsCol.Len())
	}

	// keep adminv1 imported for future extension
	_ = adminv1.MatchesWeeklyDeps{}
}
