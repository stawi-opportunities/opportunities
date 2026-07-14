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
	Archive archive.Archive // fallback when Files is nil
	Text    TextExtractor

	// Files stores the CV binary (platform files service preferred).
	Files placement.FileStore
	// Profiles writes the file-id reference on candidate_profiles.
	Profiles placement.ProfileStore
	// Placement rebuilds the match summary synchronously after extract.
	Placement *placement.Service
	// Drafts merges chat preferences and persists CV text into the draft
	// so the next chat turn can assess capabilities without re-upload.
	Drafts OnboardingDraftStore

	// MaxBytes caps the size of the uploaded file. 0 → 10 MiB default.
	MaxBytes int64
}

// UploadHandler returns an http.HandlerFunc implementing:
//
//	POST /candidates/cv/upload
//
// Flow: extract text → store file → profile file ref → placement summary
// (sync) → optional async cv-extract for LLM enrichment.
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
			"accepted":          true,
			"candidate_id":      candidateID,
			"cv_version":        result.Version,
			"file_id":           result.FileID,
			"content_uri":       result.ContentURI,
			"content_hash":      result.ContentHash,
			"storage":           result.Storage,
			"cv_length":         result.TextLength,
			"extracted_text":    truncateRunesForResponse(result.ExtractedText, 40_000),
			"placement_summary": result.PlacementSummary,
			"placement_ready":   result.PlacementReady,
			"missing":           result.Missing,
		})
	}
}

type cvUploadInput struct {
	CandidateID string
	Filename    string
	ContentType string
	Body        []byte
}

type cvUploadResult struct {
	Version          int
	FileID           string
	ContentURI       string
	ContentHash      string
	Storage          string
	SizeBytes        int64
	ExtractedText    string
	TextLength       int
	PlacementSummary string
	PlacementReady   bool
	Missing          []string
}

type processErr struct {
	Code    string
	Message string
	Err     error
}

func (e *processErr) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// processCVUpload (synchronous path for chat):
//  1. Extract plain text from the file
//  2. Store bytes in the files service (archive fallback)
//  3. Save file-id reference on candidate_profiles
//  4. Merge prefs from chat draft + CV text → placement summary (sync)
//  5. Persist CV text into onboarding draft so chat readiness works
//  6. Best-effort enqueue async LLM cv-extract (enrichment only)
func processCVUpload(ctx context.Context, deps UploadDeps, in cvUploadInput) (*cvUploadResult, error) {
	log := util.Log(ctx)

	// 1. Extract text — required for immediate chat / placement.
	text, err := extractText(deps.Text, in.Filename, in.Body)
	if err != nil {
		return nil, &processErr{Code: "text_extraction_failed", Message: err.Error(), Err: err}
	}
	if strings.TrimSpace(text) == "" {
		return nil, &processErr{Code: "empty_cv", Message: "extracted cv text is empty"}
	}
	textLen := len([]rune(text))

	// 2. Store binary in files service (or archive fallback).
	files := deps.Files
	if files == nil && deps.Archive != nil {
		files = &placement.ArchiveFileStore{Archive: deps.Archive}
	}
	if files == nil {
		return nil, &processErr{Code: "store_failed", Message: "no file store configured"}
	}
	ref, err := files.Put(ctx, in.CandidateID, in.Filename, in.ContentType, in.Body)
	if err != nil {
		if deps.Archive != nil && ref.Storage != "archive" {
			log.WithError(err).Warn("upload: files service failed; falling back to archive")
			ref, err = (&placement.ArchiveFileStore{Archive: deps.Archive}).Put(
				ctx, in.CandidateID, in.Filename, in.ContentType, in.Body)
		}
		if err != nil {
			return nil, &processErr{Code: "store_failed", Message: "could not store cv in files service", Err: err}
		}
	}

	// 3. Profile file-id reference (best-effort if profile not created yet).
	if deps.Profiles != nil {
		if err := deps.Profiles.SetCVFileRef(ctx, in.CandidateID, placement.ProfileCV{
			FileID:      ref.FileID,
			ContentURI:  ref.ContentURI,
			ContentHash: ref.ContentHash,
			CVURL:       ref.ContentURI,
		}); err != nil {
			log.WithError(err).WithField("candidate_id", in.CandidateID).
				Warn("upload: profile file ref not updated")
		}
	}

	// 4. Placement summary — merge chat preferences + CV text (synchronous).
	pf := placement.Fields{ExtraInfo: text}
	var stored onboardingEnvelope
	if deps.Drafts != nil {
		if env, eErr := loadOnboardingEnvelope(ctx, deps.Drafts, in.CandidateID); eErr == nil {
			stored = env
			pf = toPlacementFields(fieldsFromEnvelope(env))
			pf.ExtraInfo = text
		}
	}

	version := 1
	placementSummary := ""
	placementReady := false
	var missing []string
	if deps.Placement != nil {
		res, pErr := deps.Placement.Rebuild(ctx, placement.RebuildInput{
			CandidateID: in.CandidateID,
			Fields:      pf,
		})
		if pErr != nil {
			log.WithError(pErr).WithField("candidate_id", in.CandidateID).
				Warn("upload: placement rebuild failed")
		} else if res != nil {
			version = res.Version
			placementSummary = res.Document.SummaryText
			placementReady = res.Document.Ready
			missing = res.Document.Missing
		}
	}
	if missing == nil {
		missing = placement.MissingRequired(pf)
	}

	// 5. Persist CV into onboarding draft so chat field_status sees capabilities.
	if deps.Drafts != nil {
		mergedFields := fieldsFromEnvelope(stored)
		mergedFields.ExtraInfo = truncateRunes(text, 8000)
		if err := persistChatSession(ctx, MeChatDeps{Drafts: deps.Drafts, Now: nil},
			in.CandidateID, stored, mergedFields, stored.Messages, placementReady); err != nil {
			log.WithError(err).WithField("candidate_id", in.CandidateID).
				Warn("upload: draft CV text persist failed")
		}
	}

	// 6. Async LLM enrich (non-blocking for chat). Failure is logged only after
	// the sync placement path already succeeded.
	if deps.Svc != nil {
		if err := enqueueCVExtract(ctx, deps.Svc, cvUploadEnqueue{
			CandidateID:   in.CandidateID,
			CVVersion:     version,
			RawArchiveRef: ref.ContentURI,
			Filename:      in.Filename,
			ContentType:   in.ContentType,
			SizeBytes:     ref.SizeBytes,
			ExtractedText: text,
			FileID:        ref.FileID,
			ContentURI:    ref.ContentURI,
			ContentHash:   ref.ContentHash,
			Storage:       ref.Storage,
		}); err != nil {
			log.WithError(err).WithField("candidate_id", in.CandidateID).
				Warn("upload: async cv-extract enqueue failed (sync path ok)")
		}
	}

	return &cvUploadResult{
		Version:          version,
		FileID:           ref.FileID,
		ContentURI:       ref.ContentURI,
		ContentHash:      ref.ContentHash,
		Storage:          ref.Storage,
		SizeBytes:        ref.SizeBytes,
		ExtractedText:    text,
		TextLength:       textLen,
		PlacementSummary: placementSummary,
		PlacementReady:   placementReady,
		Missing:          missing,
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
	case "store_failed":
		http.Error(w, `{"error":"store failed"}`, http.StatusBadGateway)
	default:
		http.Error(w, `{"error":"upload failed"}`, http.StatusInternalServerError)
	}
}

func truncateRunesForResponse(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// errTooLarge signals that the uploaded file exceeded the byte cap.
var errTooLarge = errors.New("file too large")

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
		return stripRTF(string(body)), nil
	default:
		return "", errors.New("unsupported file type (pdf, docx, txt, rtf accepted)")
	}
}

func stripRTF(s string) string {
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
		case !inCtrl:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}
