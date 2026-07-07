package main

import (
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

type digestAdminHandler struct{ trace *repository.TraceRepository }

func registerDigestAdmin(mux *http.ServeMux, trace *repository.TraceRepository) {
	h := digestAdminHandler{trace: trace}
	mux.HandleFunc("GET /admin/trace/seeds/{id}/digest", requireAdmin(h.Digest))
}

func (h digestAdminHandler) Digest(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")
	if sourceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "source id required")
		return
	}
	dateString := r.URL.Query().Get("date")
	if dateString == "" {
		dateString = time.Now().UTC().Format("2006-01-02")
	}
	day, err := time.Parse("2006-01-02", dateString)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "date must be YYYY-MM-DD")
		return
	}
	summary, err := h.trace.DayDigest(r.Context(), sourceID, day.UTC(), day.UTC().Add(24*time.Hour))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_id": sourceID, "date": dateString, "data_source": "postgres",
		"crawl_jobs": summary.CrawlJobs, "variants_emitted": summary.VariantsEmitted,
		"variants_rejected": summary.VariantsRejected, "variants_published": summary.VariantsPublished,
		"rejection_reasons": summary.RejectionReasons,
	})
}
