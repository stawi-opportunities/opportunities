package v1

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// MeCVGetHandler serves GET /me/cv — file-id reference from the profile plus
// qualifications text from the placement summary (no separate CV document store).
func MeCVGetHandler(deps UploadDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use GET")
			return
		}
		ctx := r.Context()
		candidateID := httpmw.CandidateFromContext(ctx)

		fileID, contentURI, contentHash := "", "", ""
		if deps.Profiles != nil {
			ref, err := deps.Profiles.GetCVFileRef(ctx, candidateID)
			if err != nil {
				util.Log(ctx).WithError(err).Warn("me/cv GET: profile read failed")
			} else {
				fileID = ref.FileID
				contentURI = ref.ContentURI
				contentHash = ref.ContentHash
			}
		}

		extracted := ""
		version := 0
		ready := false
		if deps.Placement != nil && deps.Placement.Store != nil {
			if doc, err := deps.Placement.Store.Get(ctx, candidateID); err == nil && doc != nil {
				version = doc.Version
				ready = doc.Ready
				// Qualifications section is the CV corpus used for capabilities.
				extracted = strings.TrimSpace(strings.TrimPrefix(doc.QualificationsText, "## Qualifications"))
				extracted = strings.TrimSpace(extracted)
				if extracted == "(CV not yet provided)" {
					extracted = ""
				}
			}
		}

		present := fileID != "" || contentURI != "" || extracted != ""
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":              true,
			"present":         present,
			"cv_version":      version,
			"file_id":         fileID,
			"content_uri":     contentURI,
			"content_hash":    contentHash,
			"cv_length":       len([]rune(extracted)),
			"extracted_text":  truncateRunesForResponse(extracted, 40_000),
			"placement_ready": ready,
		})
	}
}

// MeCVHandler serves PUT /me/cv — the logged-in candidate's CV upload.
//
// Synchronous path (required for chat to continue immediately):
//  1. Extract plain text
//  2. Store binary in the files service (archive fallback)
//  3. Save file-id on candidate_profiles
//  4. Rebuild placement summary (qualifications + preferences)
//  5. Persist CV text into chat draft for readiness
//
// Async: enqueue cv-extract for LLM enrichment only.
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
			"ok":                true,
			"cv_length":         result.TextLength,
			"filename":          hdr.Filename,
			"extracted_text":    truncateRunesForResponse(result.ExtractedText, 40_000),
			"cv_version":        result.Version,
			"file_id":           result.FileID,
			"content_uri":       result.ContentURI,
			"content_hash":      result.ContentHash,
			"storage":           result.Storage,
			"placement_summary": result.PlacementSummary,
			"placement_ready":   result.PlacementReady,
			"missing":           result.Missing,
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
	case "store_failed":
		httpmw.ProblemJSON(w, http.StatusBadGateway, "store_failed", pe.Message)
	default:
		httpmw.ProblemJSON(w, http.StatusBadGateway, "upload_failed", pe.Message)
	}
}
