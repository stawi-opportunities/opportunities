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

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/profilecontacts"
)

// memContactDir records EnsureDetails calls (standalone CreateContact stand-in).
type memContactDir struct {
	details []string
	known   []string
	calls   int
}

func (m *memContactDir) EnsureDetails(_ context.Context, details, knownIDs []string) ([]profilecontacts.Ref, error) {
	m.calls++
	m.details = append([]string(nil), details...)
	m.known = append([]string(nil), knownIDs...)
	out := make([]profilecontacts.Ref, 0, len(knownIDs)+len(details))
	for _, id := range knownIDs {
		out = append(out, profilecontacts.Ref{ID: id})
	}
	for i, d := range details {
		out = append(out, profilecontacts.Ref{ID: "ct_" + string(rune('a'+i)), Detail: d})
	}
	return out, nil
}

func (m *memContactDir) Resolve(_ context.Context, ids []string) ([]profilecontacts.Ref, []string, error) {
	out := make([]profilecontacts.Ref, 0, len(ids))
	for _, id := range ids {
		out = append(out, profilecontacts.Ref{ID: id})
	}
	return out, nil, nil
}

func TestMeCVHandlerArchivesAndEnqueues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	col := &queueCollector{}
	contacts := &memContactDir{}

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

	handler := httpmw.NewCandidateAuth(nil)(MeCVHandler(UploadDeps{
		Svc:       svc,
		Archive:   archive.NewFakeArchive(),
		Text:      &fakeTextExtractor{out: "Jane Doe\njane@example.com\n+256700111222\nresume plain text content long enough to pass"},
		Structure: fakeStructure{},
		Contacts:  contacts,
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
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["fully_processed"] != true {
		t.Fatalf("fully_processed=%v body=%s", body["fully_processed"], rec.Body.String())
	}
	if body["structure_source"] != "ai" {
		t.Fatalf("structure_source=%v", body["structure_source"])
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
	// Standalone CV contacts (not attached to person profile).
	if contacts.calls != 1 {
		t.Fatalf("contact EnsureDetails calls=%d want 1", contacts.calls)
	}
	foundEmail := false
	for _, d := range contacts.details {
		if d == "test@example.com" {
			foundEmail = true
		}
	}
	if !foundEmail {
		t.Fatalf("expected structure email detail in EnsureDetails, got %v", contacts.details)
	}
	if pcs, ok := body["platform_contacts"].([]any); !ok || len(pcs) == 0 {
		t.Fatalf("platform_contacts missing in response: %v", body["platform_contacts"])
	}
}

func TestMeCVHandlerRejectsMissingFilePart(t *testing.T) {
	_, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("me-cv-missing"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(context.Background())

	handler := httpmw.NewCandidateAuth(nil)(MeCVHandler(UploadDeps{
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
