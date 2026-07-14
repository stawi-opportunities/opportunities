package v1

import (
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// MeCVGetHandler serves GET /me/cv — returns the current local CV document
// index (file pointers + extracted text) so chat and settings can resume
// without re-uploading.
func MeCVGetHandler(deps UploadDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use GET")
			return
		}
		candidateID := httpmw.CandidateFromContext(r.Context())
		if deps.Index == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "present": false})
			return
		}
		doc, err := deps.Index.GetCurrent(r.Context(), candidateID)
		if err != nil {
			util.Log(r.Context()).WithError(err).Warn("me/cv GET: index read failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "index_read_failed", "could not load cv index")
			return
		}
		if doc == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "present": false})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"present":        true,
			"cv_version":     doc.Version,
			"file_id":        doc.FileID,
			"content_uri":    doc.ContentURI,
			"content_hash":   doc.ContentHash,
			"filename":       doc.Filename,
			"content_type":   doc.ContentType,
			"size_bytes":     doc.SizeBytes,
			"storage":        doc.Storage,
			"cv_length":      doc.TextLength,
			"extracted_text": truncateRunesForResponse(doc.ExtractedText, 40_000),
			"updated_at":     doc.UpdatedAt,
		})
	}
}

// MeCVHandler serves PUT /me/cv — the logged-in candidate's CV upload
// from the dashboard / chat. Unlike POST /candidates/cv/upload (multipart
// with a candidate_id FORM field), this route derives the candidate from
// the JWT subject and accepts the CV as the `file` part the auth-runtime's
// upload() helper sends (FormData with a single "file" field; method PUT).
//
// Pipeline:
//  1. Extract plain text (PDF/DOCX/txt/rtf)
//  2. Store raw bytes in the platform files service (or R2 archive fallback)
//  3. Upsert local candidate_cv_documents index from extracted text
//  4. Best-effort profile CV pointers (cv_storage_uri / cv_content_hash)
//  5. Enqueue cv-extract → embed → candidate_match_indexes gap-fill
func MeCVHandler(deps UploadDeps) http.HandlerFunc {
	maxBytes := deps.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10 MiB
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPut {
			w.Header().Set("Allow", "PUT")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use PUT")
			return
		}

		candidateID := httpmw.CandidateFromContext(ctx)

		if err := r.ParseMultipartForm(maxBytes); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"parse_multipart_failed", "could not parse multipart body")
			return
		}

		file, hdr, err := r.FormFile("file")
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"cv_file_required", "cv file part 'file' is required")
			return
		}
		defer func() { _ = file.Close() }()

		body, err := readBounded(file, maxBytes)
		if err != nil {
			if err == errTooLarge {
				httpmw.ProblemJSON(w, http.StatusRequestEntityTooLarge,
					"file_too_large", "cv file exceeds the size limit")
				return
			}
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"body_read_failed", "could not read cv file")
			return
		}

		result, err := processCVUpload(ctx, deps, cvUploadInput{
			CandidateID: candidateID,
			Filename:    hdr.Filename,
			ContentType: hdr.Header.Get("Content-Type"),
			Body:        body,
		})
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Warn("me/cv: process failed")
			writeMeCVProcessError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"cv_length":      result.TextLength,
			"filename":       hdr.Filename,
			"extracted_text": truncateRunesForResponse(result.ExtractedText, 40_000),
			"cv_version":     result.Version,
			"file_id":        result.FileID,
			"content_uri":    result.ContentURI,
			"content_hash":   result.ContentHash,
			"storage":        result.Storage,
		})
	}
}

func writeMeCVProcessError(w http.ResponseWriter, err error) {
	pe, ok := err.(*processErr)
	if !ok {
		httpmw.ProblemJSON(w, http.StatusBadGateway, "upload_failed", "could not process cv")
		return
	}
	switch pe.Code {
	case "text_extraction_failed":
		httpmw.ProblemJSON(w, http.StatusUnprocessableEntity, "text_extraction_failed", pe.Message)
	case "empty_cv":
		httpmw.ProblemJSON(w, http.StatusUnprocessableEntity, "empty_cv", pe.Message)
	case "archive_failed":
		httpmw.ProblemJSON(w, http.StatusBadGateway, "archive_failed", pe.Message)
	case "index_failed":
		httpmw.ProblemJSON(w, http.StatusBadGateway, "index_failed", pe.Message)
	case "publish_failed":
		httpmw.ProblemJSON(w, http.StatusBadGateway, "publish_failed", pe.Message)
	default:
		httpmw.ProblemJSON(w, http.StatusBadGateway, "upload_failed", pe.Message)
	}
}
