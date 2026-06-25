package v1

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// MeCVHandler serves PUT /me/cv — the logged-in candidate's CV upload
// from the dashboard. Unlike POST /candidates/cv/upload (multipart with a
// candidate_id FORM field), this route derives the candidate from the JWT
// subject claim and accepts the CV as the `file` part the auth-runtime's
// upload() helper sends (FormData with a single "file" field; method PUT).
//
// It runs the SAME downstream pipeline as UploadHandler: archive the raw
// bytes → extract plain text → enqueue cv-extract, which fans out to
// cv-embed → CandidateEmbeddingV1 → CandidateChangeConsumer gap-fill, so a
// candidate who PUTs their CV ends up with a candidate_match_indexes
// embedding and refreshed candidate_matches.
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

		// The auth-runtime upload() helper posts the CV under the "file"
		// form field (Blob with the original filename + content-type).
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

		hash, size, err := deps.Archive.PutRaw(ctx, body)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("me/cv: PutRaw failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway,
				"archive_failed", "could not archive cv")
			return
		}

		text, err := extractText(deps.Text, hdr.Filename, body)
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).
				Warn("me/cv: text extraction failed")
			httpmw.ProblemJSON(w, http.StatusUnprocessableEntity,
				"text_extraction_failed", err.Error())
			return
		}
		if strings.TrimSpace(text) == "" {
			httpmw.ProblemJSON(w, http.StatusUnprocessableEntity,
				"empty_cv", "extracted cv text is empty")
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
			log.WithError(err).WithField("candidate_id", candidateID).
				Error("me/cv: enqueue cv-extract failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway,
				"publish_failed", "could not enqueue cv for processing")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"cv_length": len(text),
		})
	}
}
