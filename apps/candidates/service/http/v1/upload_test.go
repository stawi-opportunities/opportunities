package v1

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
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/frametests"

	"stawi.jobs/pkg/archive"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// uploadCollector captures emitted CVUploadedV1 envelopes.
type uploadCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.CVUploadedV1]
}

func (c *uploadCollector) Name() string     { return eventsv1.TopicCVUploaded }
func (c *uploadCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *uploadCollector) Validate(context.Context, any) error { return nil }
func (c *uploadCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVUploadedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *uploadCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

// fakeTextExtractor returns a hard-coded plain-text extraction.
type fakeTextExtractor struct{ out string }

func (f *fakeTextExtractor) FromPDF(_ []byte) (string, error)  { return f.out, nil }
func (f *fakeTextExtractor) FromDOCX(_ []byte) (string, error) { return f.out, nil }

func TestUploadHandlerArchivesAndEmits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-upload-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &uploadCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	deps := UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeTextExtractor{out: "resume plain text content long enough to pass"},
	}
	handler := UploadHandler(deps)

	// Build a multipart body.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("candidate_id", "cnd_test")
	fw, _ := mw.CreateFormFile("cv", "resume.pdf")
	_, _ = fw.Write([]byte("%PDF-1.4 fake content"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/candidates/cv/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_test" || p.CVVersion != 1 || p.RawArchiveRef == "" || p.ExtractedText == "" {
		t.Fatalf("bad payload: %+v", p)
	}

	_ = events.EventI(col) // keep import referenced
}

func TestUploadHandlerRejectsMissingCandidateID(t *testing.T) {
	_, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("cv-upload-missing"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(context.Background())

	handler := UploadHandler(UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeTextExtractor{out: "x"},
	})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("cv", "resume.pdf")
	_, _ = fw.Write([]byte("content"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/candidates/cv/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
