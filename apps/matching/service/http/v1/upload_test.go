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
	"github.com/pitabwire/frame/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// queueCollector implements queue.SubscribeWorker. It captures
// payloads published to the cv-extract subject so the test can
// assert the upload handler enqueued the right envelope.
type queueCollector struct {
	mu  sync.Mutex
	got [][]byte
}

func (c *queueCollector) Handle(_ context.Context, _ map[string]string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, append([]byte(nil), payload...))
	return nil
}

func (c *queueCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

// fakeTextExtractor returns a hard-coded plain-text extraction.
type fakeTextExtractor struct{ out string }

func (f *fakeTextExtractor) FromPDF(_ []byte) (string, error)  { return f.out, nil }
func (f *fakeTextExtractor) FromDOCX(_ []byte) (string, error) { return f.out, nil }

func TestUploadHandlerArchivesAndEnqueues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	col := &queueCollector{}

	const memURL = "mem://cv-extract-test"
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("cv-upload-test"),
		frametests.WithNoopDriver(),
		frame.WithRegisterPublisher(eventsv1.SubjectCVExtract, memURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectCVExtract, memURL, col),
	)
	defer svc.Stop(ctx)

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(300 * time.Millisecond)

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

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("enqueued=%d, want 1", col.Len())
	}
	col.mu.Lock()
	rawEnvelope := col.got[0]
	col.mu.Unlock()

	var env eventsv1.Envelope[eventsv1.CVUploadedV1]
	if err := json.Unmarshal(rawEnvelope, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p := env.Payload
	if p.CandidateID != "cnd_test" || p.CVVersion != 1 || p.RawArchiveRef == "" || p.ExtractedText == "" {
		t.Fatalf("bad payload: %+v", p)
	}
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
