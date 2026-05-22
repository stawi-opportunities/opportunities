package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

const maxAttachmentBytes = 25 << 20 // 25 MiB

type attachmentResp struct {
	AttachmentID  string    `json:"attachment_id"`
	ApplicationID string    `json:"application_id"`
	Filename      string    `json:"filename,omitempty"`
	ContentType   string    `json:"content_type"`
	Bytes         int64     `json:"bytes"`
	CreatedAt     time.Time `json:"created_at"`
}

func toAttachmentResp(a *applications.Attachment) attachmentResp {
	return attachmentResp{
		AttachmentID: a.AttachmentID, ApplicationID: a.ApplicationID,
		Filename: a.Filename, ContentType: a.ContentType,
		Bytes: a.Bytes, CreatedAt: a.CreatedAt,
	}
}

func uploadAttachment(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		// limit request body
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "multipart parse: "+err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			ProblemJSON(w, http.StatusBadRequest, "bad_input", "file field required")
			return
		}
		defer file.Close()

		attID := d.newID()
		r2Key := "applications/" + app.CandidateID + "/" + app.ApplicationID + "/" + attID + "-" + header.Filename
		contentType := header.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		n, err := d.BlobStore.Put(r.Context(), r2Key, contentType, file)
		if err != nil {
			ProblemFromError(w, fmt.Errorf("attachment upload: %w", err))
			return
		}
		row, err := d.AttachmentsStore.Create(r.Context(), applications.Attachment{
			AttachmentID:  attID,
			ApplicationID: app.ApplicationID,
			R2Key:         r2Key,
			ContentType:   contentType,
			Bytes:         n,
			Filename:      header.Filename,
		})
		if err != nil {
			// best-effort blob cleanup
			_ = d.BlobStore.Delete(r.Context(), r2Key)
			ProblemFromError(w, err)
			return
		}
		_ = d.EventLog.Write(r.Context(), applications.Event{
			EventID:       d.newID(),
			OccurredAt:    d.now(),
			ApplicationID: app.ApplicationID,
			CandidateID:   app.CandidateID,
			Kind:          applications.EventKindAttachmentAdded,
			Actor:         applications.ActorExtension,
			Data: map[string]any{
				"attachment_id": attID, "filename": header.Filename, "bytes": n,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toAttachmentResp(row))
	}
}

func deleteAttachment(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, stop := loadOwnedApplication(w, r, d, r.PathValue("id"))
		if stop {
			return
		}
		attID := r.PathValue("att_id")
		existing, err := d.AttachmentsStore.Get(r.Context(), attID)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if existing.ApplicationID != app.ApplicationID {
			ProblemJSON(w, http.StatusNotFound, "not_found", "attachment not found")
			return
		}
		if err := d.AttachmentsStore.SoftDelete(r.Context(), attID); err != nil {
			ProblemFromError(w, err)
			return
		}
		_ = d.BlobStore.Delete(r.Context(), existing.R2Key) // best-effort
		_ = d.EventLog.Write(r.Context(), applications.Event{
			EventID:       d.newID(),
			OccurredAt:    d.now(),
			ApplicationID: app.ApplicationID,
			CandidateID:   app.CandidateID,
			Kind:          applications.EventKindAttachmentDeleted,
			Actor:         applications.ActorExtension,
			Data:          map[string]any{"attachment_id": attID},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

func presignAttachment(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cand := CandidateFromContext(r.Context())
		attID := r.PathValue("att_id")
		att, err := d.AttachmentsStore.Get(r.Context(), attID)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if att.DeletedAt != nil {
			ProblemJSON(w, http.StatusNotFound, "not_found", "attachment not found")
			return
		}
		// Ownership via the parent application's candidate.
		app, err := d.Store.GetByID(r.Context(), att.ApplicationID)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		if app.CandidateID != cand {
			ProblemJSON(w, http.StatusNotFound, "not_found", "attachment not found")
			return
		}
		url, err := d.BlobStore.PresignGet(r.Context(), att.R2Key, 15*time.Minute)
		if err != nil {
			ProblemFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"download_url": url})
	}
}
