package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

func TestMeCVHandlerArchivesAndEnqueues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	col := &queueCollector{}

	const memURL = "mem://me-cv-extract-test"
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("me-cv-test"),
		frametests.WithNoopDriver(),
		frame.WithRegisterPublisher(eventsv1.SubjectCVExtract, memURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectCVExtract, memURL, col),
	)
	defer svc.Stop(ctx)

	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, eventsv1.SubjectCVExtract, 2*time.Second)

	handler := httpmw.CandidateAuth(MeCVHandler(UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeTextExtractor{out: "resume plain text content long enough to pass"},
	}))

	// The auth-runtime upload() helper PUTs a multipart body with the CV
	// under the "file" field.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "resume.pdf")
	_, _ = fw.Write([]byte("%PDF-1.4 fake content"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPut, "/me/cv", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Candidate-ID", "cand_me_cv_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

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
	if p.CandidateID != "cand_me_cv_1" || p.RawArchiveRef == "" || p.ExtractedText == "" {
		t.Fatalf("bad payload: %+v", p)
	}
}

func TestMeCVHandlerRejectsMissingFilePart(t *testing.T) {
	_, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("me-cv-missing"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(context.Background())

	handler := httpmw.CandidateAuth(MeCVHandler(UploadDeps{
		Svc:     svc,
		Archive: archive.NewFakeArchive(),
		Text:    &fakeTextExtractor{out: "x"},
	}))

	// Multipart body with the wrong field name → 400.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("cv", "resume.pdf")
	_, _ = fw.Write([]byte("content"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPut, "/me/cv", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Candidate-ID", "cand_x")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
