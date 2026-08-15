// Package v1 contains the Phase 5 HTTP handlers for apps/matching.
// Each handler is a factory returning an http.HandlerFunc bound to its
// dependencies; no global state.
package v1

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/cv"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
	"github.com/stawi-opportunities/opportunities/pkg/placement"
	"github.com/stawi-opportunities/opportunities/pkg/profilecontacts"
)

// TextExtractor abstracts plain-text extraction for PDF / DOCX bytes.
// Real impl wraps pkg/extraction.ExtractTextFromPDF and
// ExtractTextFromDOCX; tests can inject a deterministic fake.
type TextExtractor interface {
	FromPDF(data []byte) (string, error)
	FromDOCX(data []byte) (string, error)
}

// CVStructureExtractor turns plain CV text into structured profile fields.
// Production wires extraction.Extractor; tests inject a fake.
type CVStructureExtractor interface {
	ExtractCV(ctx context.Context, text string) (*extraction.CVFields, error)
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

	// Structure optionally runs AI section extraction synchronously so the
	// CV hub fills missing fields without a second manual edit pass.
	Structure CVStructureExtractor
	// DB is used to read/write candidate_profiles hub fields after extract.
	DB *sql.DB
	// Contacts creates standalone ProfileService contacts for CV details
	// (not attached to the person profile). Checkout/notify use only
	// profile-attached identity contacts via ProfileID.
	Contacts profilecontacts.Directory

	// MaxBytes caps the size of the uploaded file. 0 → 10 MiB default.
	MaxBytes int64
	// StructureTimeout bounds the required sync AI section extract. 0 → 45s.
	StructureTimeout time.Duration
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
		_ = json.NewEncoder(w).Encode(uploadResponseMap(candidateID, result))
	}
}

func uploadResponseMap(candidateID string, result *cvUploadResult) map[string]any {
	out := map[string]any{
		"accepted":     true,
		"ok":           true,
		"candidate_id": candidateID,
		"profile_id":   result.ProfileID,
		"cv_version":   result.Version,
		"file_id":      result.FileID,
		"content_uri":  result.ContentURI,
		"content_hash": result.ContentHash,
		"storage":      result.Storage,
		"cv_length":    result.TextLength,
		// Keep full extracted CV text for the client (no silent truncation).
		"extracted_text":    result.ExtractedText,
		"placement_summary": result.PlacementSummary,
		"placement_ready":   result.PlacementReady,
		"missing":           result.Missing,
		"filled_fields":     result.FilledFields,
		"structure_source":  result.StructureSource,
		// fully_processed is true only after required sync AI sectioning + merge.
		"fully_processed": result.FullyProcessed,
	}
	// Standalone CV contact IDs only — no phone/email plaintext in the response bag.
	if len(result.PlatformContacts) > 0 {
		out["platform_contacts"] = result.PlatformContacts
		out["cv_contact_ids"] = profilecontacts.IDs(result.PlatformContacts)
	}
	if result.ProfileFields != nil {
		out["profile_fields"] = result.ProfileFields
	}
	return out
}

type cvUploadInput struct {
	CandidateID string
	Filename    string
	ContentType string
	Body        []byte
}

type cvUploadResult struct {
	Version          int
	ProfileID        string
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
	// FilledFields lists profile-field keys auto-filled from this upload.
	FilledFields []string
	// ProfileFields is the post-merge hub bag (for immediate UI hydration).
	// Never includes phone/email plaintext.
	ProfileFields *candidatestore.ProfileFields
	// StructureSource is always "ai" when fully_processed.
	StructureSource string
	// PlatformContacts are standalone CV contact refs (CreateContact IDs).
	// Not used for checkout/notify — those use profile-attached contacts only.
	PlatformContacts []profilecontacts.Ref
	// FullyProcessed means sync AI sectioning + profile merge completed.
	FullyProcessed bool
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

// processCVUpload is the synchronous path for chat + CV hub.
//
// A CV is not fully processed until AI sectioning has completed and missing
// hub fields have been merged. Order:
//  1. Plain-text extract from file
//  2. Required sync AI section extract (fail closed if unavailable)
//  3. Merge only empty profile fields + persist hub bag
//  4. Store binary with platform profile_id as files accessor_id
//  5. Placement rebuild + draft persist (sync)
//  6. Optional async queue for embed/score only (not required for "done")
func processCVUpload(ctx context.Context, deps UploadDeps, in cvUploadInput) (*cvUploadResult, error) {
	log := util.Log(ctx)

	profileID := notify.ProfileID(ctx, deps.DB, in.CandidateID)
	if profileID == "" {
		profileID = in.CandidateID
	}

	// 1. Plain text — required.
	text, err := extractText(deps.Text, in.Filename, in.Body)
	if err != nil {
		return nil, &processErr{Code: "text_extraction_failed", Message: err.Error(), Err: err}
	}
	if strings.TrimSpace(text) == "" {
		return nil, &processErr{Code: "empty_cv", Message: "extracted cv text is empty"}
	}
	textLen := len([]rune(text))

	// 2. Required AI sectioning — upload is not complete without this.
	if deps.Structure == nil {
		return nil, &processErr{
			Code:    "structure_unavailable",
			Message: "AI CV sectioning is not configured; cannot fully process upload",
		}
	}
	timeout := deps.StructureTimeout
	if timeout <= 0 {
		// Long CVs + exhaustive sectioning (full bullets, skills, certs).
		timeout = 90 * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	extracted, sErr := deps.Structure.ExtractCV(sctx, text)
	cancel()
	if sErr != nil {
		return nil, &processErr{
			Code:    "structure_failed",
			Message: "AI could not section the CV",
			Err:     sErr,
		}
	}
	if extracted == nil {
		return nil, &processErr{
			Code:    "structure_failed",
			Message: "AI returned empty CV sections",
		}
	}

	// Heuristic contact + section fill gaps AI may miss.
	contact := cv.ParseContactFromText(text)

	// 3. Merge empty hub fields and persist synchronously.
	var existing *candidatestore.ProfileFields
	if deps.DB != nil {
		var gErr error
		existing, _, gErr = candidatestore.GetProfileFields(ctx, deps.DB, in.CandidateID)
		if gErr != nil && !errors.Is(gErr, candidatestore.ErrProfileNotFound) {
			return nil, &processErr{
				Code:    "profile_read_failed",
				Message: "could not load existing profile fields",
				Err:     gErr,
			}
		}
	}
	hub, filled := cv.MergeExtractedIntoProfileWithText(existing, extracted, contact, text)
	if hub == nil {
		return nil, &processErr{Code: "structure_failed", Message: "profile merge produced empty result"}
	}
	hub.CandidateID = in.CandidateID
	if deps.DB != nil {
		if pErr := candidatestore.PutProfileFields(ctx, deps.DB, in.CandidateID, hub); pErr != nil {
			return nil, &processErr{
				Code:    "profile_write_failed",
				Message: "could not save structured CV fields",
				Err:     pErr,
			}
		}
	}
	log.WithField("candidate_id", in.CandidateID).
		WithField("profile_id", profileID).
		WithField("filled", filled).
		Info("upload: AI sectioning complete; missing fields filled")

	// 3b. Standalone CV contacts (CreateContact) + cv_contact_ids only — no plaintext store.
	platformContacts := syncCVContacts(ctx, deps, in.CandidateID, extracted, contact)
	if hub != nil {
		hub.CVContactIDs = profilecontacts.IDs(platformContacts)
		hub.ContactDetails = nil
	}

	// 4. Store binary with platform profile_id as accessor_id.
	ref, storeErr := storeCVBytes(ctx, deps, profileID, in)
	if storeErr != nil {
		log.WithError(storeErr).WithField("profile_id", profileID).
			Warn("upload: binary store failed after AI sectioning; keeping structured fields")
		sum := sha256.Sum256(in.Body)
		hash := hex.EncodeToString(sum[:])
		ref = placement.FileRef{
			ContentHash: hash,
			ContentURI:  "local://" + hash,
			SizeBytes:   int64(len(in.Body)),
			Storage:     "local",
		}
	}
	if deps.Profiles != nil && (ref.FileID != "" || ref.ContentURI != "") {
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

	// 5. Placement + draft — sync so matching/chat see the CV immediately.
	pf := placement.Fields{ExtraInfo: text}
	var stored onboardingEnvelope
	if deps.Drafts != nil {
		if env, eErr := loadOnboardingEnvelope(ctx, deps.Drafts, in.CandidateID); eErr == nil {
			stored = env
			pf = toPlacementFields(fieldsFromEnvelope(env))
			pf.ExtraInfo = text
		}
	}
	if hub.TargetJobTitle != "" && pf.TargetJobTitle == "" {
		pf.TargetJobTitle = hub.TargetJobTitle
	}
	if hub.ExperienceLevel != "" && pf.ExperienceLevel == "" {
		pf.ExperienceLevel = hub.ExperienceLevel
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

	if deps.Drafts != nil {
		mergedFields := fieldsFromEnvelope(stored)
		// Keep full CV text for chat/matching — do not truncate.
		mergedFields.ExtraInfo = text
		if mergedFields.TargetJobTitle == "" {
			mergedFields.TargetJobTitle = hub.TargetJobTitle
		}
		if mergedFields.ExperienceLevel == "" {
			mergedFields.ExperienceLevel = hub.ExperienceLevel
		}
		chatReady := len(missingFromStatus(assessFieldStatus(mergedFields))) == 0
		if err := persistChatSession(ctx, MeChatDeps{Drafts: deps.Drafts, Now: nil},
			in.CandidateID, stored, mergedFields, stored.Messages, chatReady); err != nil {
			log.WithError(err).WithField("candidate_id", in.CandidateID).
				Warn("upload: draft CV text persist failed")
		}
	}

	// 6. Optional async embed/score pipeline only (not required for fully_processed).
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
				Warn("upload: async embed pipeline enqueue failed (sync path fully processed)")
		}
	}

	return &cvUploadResult{
		Version:          version,
		ProfileID:        profileID,
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
		FilledFields:     filled,
		ProfileFields:    hub,
		StructureSource:  "ai",
		PlatformContacts: platformContacts,
		FullyProcessed:   true,
	}, nil
}

// syncCVContacts stores CV-discovered details as standalone ProfileService
// contacts (CreateContact) and saves contact_ids on the candidate row.
// Best-effort; never fails the upload. Not for checkout/notify.
func syncCVContacts(
	ctx context.Context,
	deps UploadDeps,
	candidateID string,
	extracted *extraction.CVFields,
	heuristic cv.ParsedContact,
) []profilecontacts.Ref {
	if deps.Contacts == nil {
		return nil
	}
	var groups [][]string
	if extracted != nil {
		groups = append(groups, extracted.Emails, []string{extracted.Email}, extracted.Phones, []string{extracted.Phone})
	}
	groups = append(groups, heuristic.Emails, []string{heuristic.Email}, heuristic.Phones, []string{heuristic.Phone})
	details := profilecontacts.CollectDetails(groups...)

	var known []string
	if deps.DB != nil {
		known, _ = candidatestore.GetCVContactIDs(ctx, deps.DB, candidateID)
	}
	if len(details) == 0 && len(known) == 0 {
		return nil
	}
	refs, err := deps.Contacts.EnsureDetails(ctx, details, known)
	log := util.Log(ctx).WithField("candidate_id", candidateID)
	if err != nil {
		log.WithError(err).WithField("contacts", len(refs)).
			Warn("upload: CV contact store incomplete (CV still processed)")
	}
	ids := profilecontacts.IDs(refs)
	if deps.DB != nil && len(ids) > 0 {
		if pErr := candidatestore.PutCVContactIDs(ctx, deps.DB, candidateID, ids); pErr != nil {
			log.WithError(pErr).Warn("upload: could not persist cv_contact_ids")
		}
	}
	if len(refs) > 0 {
		log.WithField("contact_ids", ids).Info("upload: standalone CV contacts ready")
	}
	return refs
}

// storeCVBytes tries platform files (accessor = profileID), then R2 archive.
func storeCVBytes(ctx context.Context, deps UploadDeps, profileID string, in cvUploadInput) (placement.FileRef, error) {
	log := util.Log(ctx)
	files := deps.Files
	if files == nil && deps.Archive != nil {
		files = &placement.ArchiveFileStore{Archive: deps.Archive}
	}
	if files == nil {
		return placement.FileRef{}, fmt.Errorf("no file store configured")
	}
	ref, err := files.Put(ctx, profileID, in.Filename, in.ContentType, in.Body)
	if err == nil {
		return ref, nil
	}
	if deps.Archive != nil {
		log.WithError(err).Warn("upload: primary store failed; falling back to archive")
		aref, aerr := (&placement.ArchiveFileStore{Archive: deps.Archive}).Put(
			ctx, profileID, in.Filename, in.ContentType, in.Body)
		if aerr == nil {
			return aref, nil
		}
		return placement.FileRef{}, fmt.Errorf("files: %w; archive: %v", err, aerr)
	}
	return placement.FileRef{}, err
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
	case "structure_unavailable", "structure_failed":
		http.Error(w, fmt.Sprintf(`{"error":%q,"code":%q}`, pe.Message, pe.Code), http.StatusUnprocessableEntity)
	case "profile_read_failed", "profile_write_failed":
		http.Error(w, fmt.Sprintf(`{"error":%q,"code":%q}`, pe.Message, pe.Code), http.StatusBadGateway)
	case "store_failed":
		http.Error(w, `{"error":"store failed"}`, http.StatusBadGateway)
	default:
		http.Error(w, `{"error":"upload failed"}`, http.StatusInternalServerError)
	}
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
