// apps/api/cmd/trace_admin.go
//
// Admin /admin/trace/* surface — read-only views that walk the
// crawl-to-publish chain (crawl_jobs -> raw_payloads ->
// pipeline_variants -> opportunities) for operator visibility and
// rejection drill-down. All routes are gated by requireAdmin (Bearer +
// admin role) and answer in JSON.
//
// The handlers delegate to repository.TraceRepository for the
// Postgres queries; the only handler-side logic is parameter parsing,
// 404/500 mapping, and (for variants) populating the body_url shortcut
// so the UI can deep-link to /admin/raw_payloads/{id}/body without a
// second round-trip.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
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
// endpoint and may be nil otherwise (the variant/opportunity handlers
// don't read it). iceberg is optional — when nil, historic queries
// past the 7d Postgres retention silently fall back to Postgres-only.
type traceAdminHandler struct {
	trace   *repository.TraceRepository
	sources sourceLookup
	iceberg *icebergclient.Catalog
}

// registerTraceAdmin wires every /admin/trace/* route on the supplied
// mux. Called from main.go alongside registerSourcesAdmin. iceberg may
// be nil; the handler degrades gracefully.
func registerTraceAdmin(mux *http.ServeMux, trace *repository.TraceRepository, sources sourceLookup, iceberg *icebergclient.Catalog) {
	h := traceAdminHandler{trace: trace, sources: sources, iceberg: iceberg}
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

	// When the requested window crosses the 7d Postgres retention,
	// layer in Iceberg historic rejections. Soft-fails: a transient
	// catalog error logs a warning but the Postgres-only response
	// still ships. The DataSource flag tells the operator UI which
	// path supplied the numbers.
	if h.iceberg != nil && window > 6*24*time.Hour {
		augmentSummaryFromIceberg(ctx, h.iceberg, id, window, summary)
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
// Returns the full join (source + crawl_job + raw_payload + canonical
// slug) for a single variant_id. 404 when the variant isn't in
// pipeline_variants — most commonly because retention expired (7d) or
// the variant_id is a typo. The handler stamps body_url on the
// raw_payload sub-object so the UI can deep-link to
// /admin/raw_payloads/{id}/body without a second lookup.
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
			"variant not in pipeline_variants (retention may have elapsed)")
		return
	}
	if tl.RawPayload != nil && tl.RawPayload.ID != "" {
		tl.RawPayload.BodyURL = "/admin/raw_payloads/" + tl.RawPayload.ID + "/body"
	}
	writeJSON(w, http.StatusOK, tl)
}

// OpportunityTrace handles GET /admin/trace/opportunities/{slug}.
//
// Returns every pipeline_variants row joined to the canonical
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

// augmentSummaryFromIceberg layers historic rejection counts + reasons
// from the variants_rejected Iceberg table onto the Postgres summary.
// On success the summary's DataSource flips to "postgres+iceberg" and
// VariantsRejected / RejectionReasons gain the historic rows. On
// failure (transient catalog hiccup, table missing on a fresh install)
// the summary is left as the Postgres-only baseline and a warning logs.
//
// Time-windowing is applied client-side: Iceberg's variants_rejected
// has rejected_at, and we filter rows >= (now - window). This is a
// scan-and-filter — the projection keeps it cheap, but if the table
// grows past ~5k rows per scan the limit will start truncating
// historic data. Plan D can move this to a partition-pruned scan.
func augmentSummaryFromIceberg(
	ctx context.Context,
	cat *icebergclient.Catalog,
	sourceID string,
	window time.Duration,
	summary *repository.SourceSummary,
) {
	log := util.Log(ctx)
	rows, err := cat.ReadRecent(ctx, "opportunities", "variants_rejected",
		[]string{"source_id", "reasons", "rejected_at"}, 5000)
	if err != nil {
		log.WithError(err).WithField("source_id", sourceID).
			Warn("trace admin: iceberg variants_rejected read failed; serving postgres-only summary")
		return
	}
	cutoff := time.Now().Add(-window)
	for _, row := range rows {
		if !rowSourceMatches(row["source_id"], sourceID) {
			continue
		}
		if rejAt, ok := parseRowTimestamp(row["rejected_at"]); ok && rejAt.Before(cutoff) {
			continue
		}
		summary.VariantsRejected++
		for _, reason := range parseRejectionReasons(row["reasons"]) {
			summary.RejectionReasons[reason]++
		}
	}
	summary.DataSource = "postgres+iceberg"
}

// rowSourceMatches accepts the Arrow marshaled source_id and compares
// to the operator-supplied id. Arrow may marshal the string column as
// either string or []byte depending on type — accept both.
func rowSourceMatches(raw any, want string) bool {
	switch v := raw.(type) {
	case string:
		return v == want
	case []byte:
		return string(v) == want
	}
	return false
}

// parseRowTimestamp tries to coerce a marshaled Arrow timestamp into a
// time.Time. Arrow GetOneForMarshal emits timestamps as ints (nanos
// since epoch) or strings (RFC3339); we handle both. Returns (zero,
// false) on miss so the caller falls back to "include this row".
func parseRowTimestamp(raw any) (time.Time, bool) {
	switch v := raw.(type) {
	case time.Time:
		return v, true
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
		if t, err := time.Parse("2006-01-02T15:04:05.000000", v); err == nil {
			return t, true
		}
	case int64:
		// Arrow Timestamp[micros] is the writer's choice for *_at columns.
		return time.UnixMicro(v).UTC(), true
	case float64:
		return time.UnixMicro(int64(v)).UTC(), true
	}
	return time.Time{}, false
}

// parseRejectionReasons extracts the reason list from a Iceberg
// variants_rejected row. The schema is StringType — Arrow marshals as
// either a JSON-encoded string ('["missing_title", ...]') or, if the
// writer stored a plain comma-joined value, a flat string. Accept both
// shapes so old + new rows aggregate cleanly.
func parseRejectionReasons(raw any) []string {
	var body string
	switch v := raw.(type) {
	case string:
		body = v
	case []byte:
		body = string(v)
	default:
		return nil
	}
	if body == "" {
		return nil
	}
	// Try JSON array first; fall back to a single-reason string.
	var arr []string
	if err := json.Unmarshal([]byte(body), &arr); err == nil {
		return arr
	}
	return []string{body}
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


