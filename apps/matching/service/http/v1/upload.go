// Package v1 contains the Phase 5 HTTP handlers for apps/matching.
// Each handler is a factory returning an http.HandlerFunc bound to its
// dependencies; no global state.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// TextExtractor abstracts plain-text extraction for PDF / DOCX bytes.
// Real impl wraps pkg/extraction.ExtractTextFromPDF and
// ExtractTextFromDOCX; tests can inject a deterministic fake.
type TextExtractor interface {
	FromPDF(data []byte) (string, error)
	FromDOCX(data []byte) (string, error)
}

// UploadDeps bundles the collaborators for the upload handler.
type UploadDeps struct {
	Svc     *frame.Service
	Archive archive.Archive
	Text    TextExtractor

	// MaxBytes caps the size of the uploaded file. 0 → 10 MiB default.
	MaxBytes int64
}

// UploadHandler returns an http.HandlerFunc implementing:
//
//	POST /candidates/cv/upload
//	Content-Type: multipart/form-data
//	Fields:
//	  candidate_id (required, string)
//	  cv           (required, file; .pdf or .docx)
//
// Flow:
//  1. Validate candidate_id + file present.
//  2. Read file bytes (bounded by MaxBytes).
//  3. Archive raw bytes via pkg/archive → raw_archive_ref.
//  4. Extract plain text (PDF or DOCX branch based on filename).
//  5. Pick cv_version by counting existing candidates_cv_current/ rows
//     for the candidate (always +1). For v1 we take a shortcut and
//     stamp 1 unconditionally; Phase 6 reads the store to pick the
//     real next version.
//  6. Emit CVUploadedV1 via Frame.
//  7. Return 202 Accepted with a JSON body echoing candidate_id + cv_version.
func UploadHandler(deps UploadDeps) http.HandlerFunc {
	maxBytes := deps.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10 MiB
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseMultipartForm(maxBytes); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse multipart: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		candidateID := strings.TrimSpace(r.FormValue("candidate_id"))
		if candidateID == "" {
			http.Error(w, `{"error":"candidate_id is required"}`, http.StatusBadRequest)
			return
		}

		file, hdr, err := r.FormFile("cv")
		if err != nil {
			http.Error(w, `{"error":"cv file is required"}`, http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()

		body, err := readBounded(file, maxBytes)
		if err != nil {
			if err == errTooLarge {
				http.Error(w, `{"error":"file too large"}`, http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, fmt.Sprintf(`{"error":"read file: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		// Archive raw bytes.
		hash, size, err := deps.Archive.PutRaw(ctx, body)
		if err != nil {
			log.WithError(err).Error("upload: PutRaw failed")
			http.Error(w, `{"error":"archive failed"}`, http.StatusInternalServerError)
			return
		}

		text, err := extractText(deps.Text, hdr.Filename, body)
		if err != nil {
			log.WithError(err).Warn("upload: text extraction failed")
			http.Error(w, fmt.Sprintf(`{"error":"text extraction: %s"}`, err.Error()), http.StatusUnprocessableEntity)
			return
		}
		if strings.TrimSpace(text) == "" {
			http.Error(w, `{"error":"extracted text is empty"}`, http.StatusUnprocessableEntity)
			return
		}

		cvVersion := 1 // v1 shortcut; Phase 6 computes the real next version
		if err := enqueueCVExtract(ctx, deps.Svc, cvUploadInput{
			CandidateID:   candidateID,
			CVVersion:     cvVersion,
			RawArchiveRef: archive.RawKey(hash),
			Filename:      hdr.Filename,
			ContentType:   hdr.Header.Get("Content-Type"),
			SizeBytes:     size,
			ExtractedText: text,
		}); err != nil {
			log.WithError(err).Error("upload: enqueue cv-extract failed")
			http.Error(w, `{"error":"publish failed"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":     true,
			"candidate_id": candidateID,
			"cv_version":   cvVersion,
		})
	}
}

// cvUploadInput carries the fields enqueueCVExtract folds into a
// CVUploadedV1 envelope. Shared by POST /candidates/cv/upload (multipart)
// and PUT /me/cv (raw body) so both entry points drive the identical
// cv-extract → cv-embed → CandidateEmbeddingV1 → gap-fill pipeline.
type cvUploadInput struct {
	CandidateID   string
	CVVersion     int
	RawArchiveRef string
	Filename      string
	ContentType   string
	SizeBytes     int64
	ExtractedText string
}

// enqueueCVExtract marshals a CVUploadedV1 envelope and publishes it onto
// the durable cv-extract queue subject. The extract handler is a Frame
// Queue subscriber (external LLM call), not a Frame Event handler, so it
// survives restarts + retries with backoff if the AI Gateway is flaky.
func enqueueCVExtract(ctx context.Context, svc *frame.Service, in cvUploadInput) error {
	payload := eventsv1.CVUploadedV1{
		CandidateID:   in.CandidateID,
		CVVersion:     in.CVVersion,
		RawArchiveRef: in.RawArchiveRef,
		Filename:      in.Filename,
		ContentType:   in.ContentType,
		SizeBytes:     in.SizeBytes,
		ExtractedText: in.ExtractedText,
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicCVUploaded, payload)
	envBytes, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("upload: marshal cv-uploaded envelope: %w", err)
	}
	if err := svc.QueueManager().Publish(ctx, eventsv1.SubjectCVExtract, envBytes); err != nil {
		return fmt.Errorf("upload: publish cv-extract: %w", err)
	}
	return nil
}

// errTooLarge signals that the uploaded file exceeded the byte cap.
var errTooLarge = errors.New("file too large")

// readBounded reads up to maxBytes from r, returning errTooLarge if the
// source has more bytes than the cap. Shared by the multipart upload
// handler and the PUT /me/cv handler.
func readBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errTooLarge
	}
	return body, nil
}

// extractText picks PDF or DOCX extraction based on filename suffix.
// Rejects any other suffix.
func extractText(ex TextExtractor, filename string, body []byte) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return ex.FromPDF(body)
	case strings.HasSuffix(lower, ".docx"):
		return ex.FromDOCX(body)
	default:
		return "", errors.New("unsupported file type (only .pdf and .docx accepted)")
	}
}

