// apps/api/cmd/raw_payload_admin.go
//
// Admin /admin/raw_payloads/{id}/body endpoint — operator-facing drill-down
// that streams the original HTML the crawler archived in R2 for a given
// raw_payloads row. Closes the rejection investigation loop: PA-S2's
// VariantTimeline.RawPayload.BodyURL points here, so clicking it in the
// admin UI renders the source markup straight in the browser.
//
// pkg/archive.R2Archive.GetRaw already decompresses the gzipped blob
// before returning, so this handler streams plain text/html (no
// Content-Encoding gzip header — that would mislead the browser into
// inflating already-inflated bytes).
package main

import (
	"errors"
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// rawPayloadAdminHandler bundles the deps for /admin/raw_payloads/*.
type rawPayloadAdminHandler struct {
	repo *repository.CrawlRepository
	arch archive.Archive
}

// registerRawPayloadAdmin wires every /admin/raw_payloads/* route.
// Called from registerSourcesAdmin so we reuse the same Frame pool
// + Archive client without re-booting the service.
func registerRawPayloadAdmin(mux *http.ServeMux, repo *repository.CrawlRepository, arch archive.Archive) {
	h := rawPayloadAdminHandler{repo: repo, arch: arch}
	mux.HandleFunc("GET /admin/raw_payloads/{id}/body", requireAdmin(h.Body))
}

// Body returns the HTML stored in R2 for a raw_payloads row. The
// archive layer transparently gunzips before returning, so the
// response is plain text/html the browser can render directly.
// Operator-only — gated by requireAdmin.
//
// 404 on missing row OR missing content_hash (the row pre-dates the
// content-addressed archive) OR archive miss (purged by retention).
// 502 on R2 fetch failure (transient).
func (h rawPayloadAdminHandler) Body(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "id required")
		return
	}
	rp, err := h.repo.GetRawPayload(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if rp == nil {
		writeError(w, http.StatusNotFound, "not_found", "raw_payload not found")
		return
	}
	if rp.ContentHash == "" {
		writeError(w, http.StatusNotFound, "not_found", "raw_payload has no content_hash")
		return
	}
	body, err := h.arch.GetRaw(ctx, rp.ContentHash)
	if err != nil {
		if errors.Is(err, archive.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "archive blob missing for content_hash")
			return
		}
		writeError(w, http.StatusBadGateway, "archive_get_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Source-URL", rp.SourceURL)
	w.Header().Set("X-Content-Hash", rp.ContentHash)
	_, _ = w.Write(body)
}
