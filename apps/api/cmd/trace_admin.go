// apps/api/cmd/trace_admin.go
//
// Admin /admin/trace/* surface — read-only views that walk the
// crawl-to-publish chain (crawl_jobs -> job_ingest_queue -> opportunities)
// for operator visibility and
// rejection drill-down. All routes are gated by requireAdmin (Bearer +
// admin role) and answer in JSON.
//
// The handlers delegate to repository.TraceRepository for the
// Postgres queries; handler-side logic is limited to parameter parsing and
// status mapping.
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// sourceLookup is the small read slice the trace handler needs from
// the source repository. The production wiring binds this to
// *repository.SourceRepository; tests inject a fake.
type sourceLookup interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
}

// traceAdminHandler bundles the dependencies for /admin/trace/*
// routes. trace is required; sources is required for the source-trace
// endpoint and may be nil otherwise.
type traceAdminHandler struct {
	trace   *repository.TraceRepository
	sources sourceLookup
}

// registerTraceAdmin wires every /admin/trace/* route on the supplied
// mux. Called from main.go alongside registerSourcesAdmin.
func registerTraceAdmin(mux *http.ServeMux, trace *repository.TraceRepository, sources sourceLookup) {
	h := traceAdminHandler{trace: trace, sources: sources}
	mux.HandleFunc("GET /admin/trace/sources/{id}", requireAdmin(h.SourceTrace))
	mux.HandleFunc("GET /admin/trace/variants/{id}", requireAdmin(h.VariantTrace))
	mux.HandleFunc("GET /admin/trace/opportunities/{slug}", requireAdmin(h.OpportunityTrace))
}

// SourceTrace handles GET /admin/trace/sources/{id}?since=24h&limit=50.
//
// Returns the source identity, the SourceSummary aggregate over the
// window, and up to limit most-recent crawl_jobs rows. 404 when the
// source row doesn't exist (operator typo / deleted source).
func (h traceAdminHandler) SourceTrace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "source id required")
		return
	}
	window := parseWindow(r.URL.Query().Get("since"), 24*time.Hour)
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)

	src, err := h.sources.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return
	}

	summary, err := h.trace.SourceSummary(ctx, id, window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	recents, err := h.trace.RecentCrawls(ctx, id, window, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"source": map[string]any{
			"id":            src.ID,
			"type":          src.Type,
			"base_url":      src.BaseURL,
			"country":       src.Country,
			"status":        src.Status,
			"health_score":  src.HealthScore,
			"next_crawl_at": src.NextCrawlAt,
			"last_seen_at":  src.LastSeenAt,
		},
		"summary":       summary,
		"recent_crawls": recents,
	})
}

// VariantTrace handles GET /admin/trace/variants/{id}.
//
// Returns the full join (source + crawl_job + canonical
// slug) for a single variant_id. 404 when the variant isn't in
// job_ingest_queue — most commonly because retention expired or
// the variant_id is a typo.
func (h traceAdminHandler) VariantTrace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "variant id required")
		return
	}
	tl, err := h.trace.VariantTimeline(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if tl == nil {
		writeError(w, http.StatusNotFound, "not_found",
			"variant not in job_ingest_queue")
		return
	}
	writeJSON(w, http.StatusOK, tl)
}

// OpportunityTrace handles GET /admin/trace/opportunities/{slug}.
//
// Returns every processed ingestion row joined to the canonical
// (via opportunities.slug). The empty-result case is NOT 404 — an
// empty list is meaningful (canonical exists, no variants currently
// in the 7d retention window).
func (h traceAdminHandler) OpportunityTrace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "slug required")
		return
	}
	variants, err := h.trace.OpportunityVariants(ctx, slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":          slug,
		"variant_count": len(variants),
		"variants":      variants,
	})
}

// parseWindow accepts Go duration strings ("24h", "1h", "168h").
// Returns def on parse failure; clamps at 30d to keep the count(*)
// queries bounded.
func parseWindow(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	if d > 30*24*time.Hour {
		return 30 * 24 * time.Hour
	}
	return d
}
