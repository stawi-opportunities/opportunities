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

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/cvstore"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/placement"
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

	// Blob prefers the platform files service; when nil falls back to Archive.
	Blob cvstore.BlobStore
	// Index is the local CV document index (extracted text + file pointers).
	Index cvstore.IndexStore
	// Profiles optionally writes candidate_profiles CV columns.
	Profiles cvstore.ProfilePointers
	// Placement rebuilds the match summary after a new CV lands.
	Placement *placement.Service
	// Drafts optional: merge chat preferences when re-indexing after CV upload.
	Drafts OnboardingDraftStore

	// MaxBytes caps the size of the uploaded file. 0 → 10 MiB default.
	MaxBytes int64
}

// UploadHandler returns an http.HandlerFunc implementing:
//
//	POST /candidates/cv/upload
//	Content-Type: multipart/form-data
//	Fields:
//	  candidate_id (optional when authenticated; must match JWT subject)
//	  cv           (required, file; .pdf or .docx)
//
// MUST be wrapped with CandidateAuth in production. Identity is taken
// from the JWT subject when present; a form candidate_id that disagrees
// is rejected.
//
// Flow:
//  1. Resolve candidate identity (auth subject preferred).
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
		if authID, ok := candidateIDFromAuth(ctx); ok {
			if candidateID != "" && candidateID != authID {
				http.Error(w, `{"error":"candidate_id does not match authenticated subject"}`, http.StatusForbidden)
				return
			}
			candidateID = authID
		}
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

		result, err := processCVUpload(ctx, deps, cvUploadInput{
			CandidateID: candidateID,
			Filename:    hdr.Filename,
			ContentType: hdr.Header.Get("Content-Type"),
			Body:        body,
		})
		if err != nil {
			log.WithError(err).Warn("upload: process failed")
			writeUploadProcessError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":      true,
			"candidate_id":  candidateID,
			"cv_version":    result.Version,
			"file_id":       result.FileID,
			"content_uri":   result.ContentURI,
			"content_hash":  result.ContentHash,
			"storage":       result.Storage,
			"cv_length":     result.TextLength,
			"extracted_text": truncateRunesForResponse(result.ExtractedText, 40_000),
		})
	}
}

// cvUploadInput is the shared payload for processCVUpload.
type cvUploadInput struct {
	CandidateID string
	Filename    string
	ContentType string
	Body        []byte
}

// cvUploadResult is returned after blob store + local index + queue enqueue.
type cvUploadResult struct {
	Version       int
	FileID        string
	ContentURI    string
	ContentHash   string
	Storage       string
	SizeBytes     int64
	ExtractedText string
	TextLength    int
}

// processErr classifies failures for HTTP mapping.
type processErr struct {
	Code    string // archive_failed | text_extraction_failed | empty_cv | index_failed | publish_failed
	Message string
	Err     error
}

func (e *processErr) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// processCVUpload:
//  1. Extract plain text from the file
//  2. Store bytes in files service (or archive fallback)
//  3. Upsert local candidate_cv_documents index
//  4. Best-effort profile CV pointers
//  5. Enqueue cv-extract for LLM embed / match index
func processCVUpload(ctx context.Context, deps UploadDeps, in cvUploadInput) (*cvUploadResult, error) {
	log := util.Log(ctx)
	text, err := extractText(deps.Text, in.Filename, in.Body)
	if err != nil {
		return nil, &processErr{Code: "text_extraction_failed", Message: err.Error(), Err: err}
	}
	if strings.TrimSpace(text) == "" {
		return nil, &processErr{Code: "empty_cv", Message: "extracted cv text is empty"}
	}
	textLen := len([]rune(text))

	// Prefer files service; fall back to R2 archive.
	blob := deps.Blob
	if blob == nil && deps.Archive != nil {
		blob = &cvstore.ArchiveBlobStore{Archive: deps.Archive}
	}
	if blob == nil {
		return nil, &processErr{Code: "archive_failed", Message: "no blob store configured"}
	}
	ref, err := blob.Put(ctx, cvstore.BlobPut{
		CandidateID: in.CandidateID,
		Filename:    in.Filename,
		ContentType: in.ContentType,
		Body:        in.Body,
	})
	if err != nil {
		// If files service failed but archive is available, try archive.
		if deps.Archive != nil && ref.Storage != "archive" {
			log.WithError(err).Warn("upload: files blob failed; falling back to archive")
			ref, err = (&cvstore.ArchiveBlobStore{Archive: deps.Archive}).Put(ctx, cvstore.BlobPut{
				CandidateID: in.CandidateID,
				Filename:    in.Filename,
				ContentType: in.ContentType,
				Body:        in.Body,
			})
		}
		if err != nil {
			return nil, &processErr{Code: "archive_failed", Message: "could not store cv", Err: err}
		}
	}

	// Local index: full text capped for PG row size (keep enough for matching/chat).
	storeText := cvstore.TruncateRunes(text, 100_000)
	version := 1
	if deps.Index != nil {
		v, idxErr := deps.Index.UpsertDocument(ctx, cvstore.Document{
			CandidateID:   in.CandidateID,
			FileID:        ref.FileID,
			ContentURI:    ref.ContentURI,
			ContentHash:   ref.ContentHash,
			Filename:      in.Filename,
			ContentType:   in.ContentType,
			SizeBytes:     ref.SizeBytes,
			ExtractedText: storeText,
			TextLength:    textLen,
			Storage:       ref.Storage,
		})
		if idxErr != nil {
			log.WithError(idxErr).WithField("candidate_id", in.CandidateID).
				Error("upload: local cv index upsert failed")
			return nil, &processErr{Code: "index_failed", Message: "could not index cv text", Err: idxErr}
		}
		version = v
	}

	if deps.Profiles != nil {
		cvURL := ref.ContentURI
		if ref.FileID != "" {
			cvURL = ref.ContentURI
		}
		if err := deps.Profiles.SetCVPointers(ctx, in.CandidateID, ref.ContentURI, ref.ContentHash, cvURL); err != nil {
			// Profile may not exist yet during chat-first onboarding.
			log.WithError(err).WithField("candidate_id", in.CandidateID).
				Warn("upload: profile cv pointers not updated")
		}
	}

	rawRef := ref.ContentURI
	if ref.Storage == "archive" {
		rawRef = ref.ContentURI
	}
	if err := enqueueCVExtract(ctx, deps.Svc, cvUploadEnqueue{
		CandidateID:   in.CandidateID,
		CVVersion:     version,
		RawArchiveRef: rawRef,
		Filename:      in.Filename,
		ContentType:   in.ContentType,
		SizeBytes:     ref.SizeBytes,
		ExtractedText: text,
		FileID:        ref.FileID,
		ContentURI:    ref.ContentURI,
		ContentHash:   ref.ContentHash,
		Storage:       ref.Storage,
	}); err != nil {
		return nil, &processErr{Code: "publish_failed", Message: "could not enqueue cv for processing", Err: err}
	}

	// Refresh placement summary: merge prior chat preferences + new CV text.
	if deps.Placement != nil {
		pf := placement.Fields{ExtraInfo: text}
		if deps.Drafts != nil {
			if env, eErr := loadOnboardingEnvelope(ctx, deps.Drafts, in.CandidateID); eErr == nil {
				pf = toPlacementFields(fieldsFromEnvelope(env))
				// Prefer the just-extracted CV as qualifications corpus.
				pf.ExtraInfo = text
			}
		}
		if _, pErr := deps.Placement.Rebuild(ctx, placement.RebuildInput{
			CandidateID: in.CandidateID,
			Fields:      pf,
		}); pErr != nil {
			log.WithError(pErr).WithField("candidate_id", in.CandidateID).
				Warn("upload: placement rebuild after CV failed")
		}
	}

	return &cvUploadResult{
		Version:       version,
		FileID:        ref.FileID,
		ContentURI:    ref.ContentURI,
		ContentHash:   ref.ContentHash,
		Storage:       ref.Storage,
		SizeBytes:     ref.SizeBytes,
		ExtractedText: text,
		TextLength:    textLen,
	}, nil
}

type cvUploadEnqueue struct {
	CandidateID   string
	CVVersion     int
	RawArchiveRef string
	Filename      string
	ContentType   string
	SizeBytes     int64
	ExtractedText string
	FileID        string
	ContentURI    string
	ContentHash   string
	Storage       string
}

// enqueueCVExtract marshals a CVUploadedV1 envelope and publishes it onto
// the durable cv-extract queue subject.
func enqueueCVExtract(ctx context.Context, svc *frame.Service, in cvUploadEnqueue) error {
	payload := eventsv1.CVUploadedV1{
		CandidateID:   in.CandidateID,
		CVVersion:     in.CVVersion,
		RawArchiveRef: in.RawArchiveRef,
		Filename:      in.Filename,
		ContentType:   in.ContentType,
		SizeBytes:     in.SizeBytes,
		FileID:        in.FileID,
		ContentURI:    in.ContentURI,
		ContentHash:   in.ContentHash,
		Storage:       in.Storage,
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

func writeUploadProcessError(w http.ResponseWriter, err error) {
	pe, ok := err.(*processErr)
	if !ok {
		http.Error(w, `{"error":"upload failed"}`, http.StatusInternalServerError)
		return
	}
	switch pe.Code {
	case "text_extraction_failed":
		http.Error(w, fmt.Sprintf(`{"error":"text extraction: %s"}`, pe.Message), http.StatusUnprocessableEntity)
	case "empty_cv":
		http.Error(w, `{"error":"extracted text is empty"}`, http.StatusUnprocessableEntity)
	case "archive_failed":
		http.Error(w, `{"error":"archive failed"}`, http.StatusBadGateway)
	case "index_failed":
		http.Error(w, `{"error":"index failed"}`, http.StatusBadGateway)
	case "publish_failed":
		http.Error(w, `{"error":"publish failed"}`, http.StatusInternalServerError)
	default:
		http.Error(w, `{"error":"upload failed"}`, http.StatusInternalServerError)
	}
}

func truncateRunesForResponse(s string, max int) string {
	return cvstore.TruncateRunes(s, max)
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

// extractText picks PDF / DOCX / plain-text extraction based on filename.
// Rejects any other suffix.
func extractText(ex TextExtractor, filename string, body []byte) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return ex.FromPDF(body)
	case strings.HasSuffix(lower, ".docx"):
		return ex.FromDOCX(body)
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".text"), strings.HasSuffix(lower, ".md"):
		return string(body), nil
	case strings.HasSuffix(lower, ".rtf"):
		// Best-effort: strip common RTF control words for chat intake.
		return stripRTF(string(body)), nil
	default:
		return "", errors.New("unsupported file type (pdf, docx, txt, rtf accepted)")
	}
}

func stripRTF(s string) string {
	// Minimal stripper — enough for plain CVs saved as .rtf.
	var b strings.Builder
	inCtrl := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			inCtrl = true
		case inCtrl && (c == ' ' || c == '\n' || c == '\r' || c == '{' || c == '}'):
			inCtrl = false
			if c == ' ' || c == '\n' || c == '\r' {
				b.WriteByte(' ')
			}
		case c == '{' || c == '}':
			// group markers
		case !inCtrl:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}
