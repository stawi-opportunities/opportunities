// apps/api/cmd/digest_admin.go
//
// Admin GET /admin/trace/seeds/{id}/digest?date=YYYY-MM-DD — per-day
// rollup of crawl jobs + variant emission/rejection/publish counts for a
// single source. Postgres covers the last 7 days; older dates hit Iceberg.
//
// The route fits under /admin/trace/seeds (operator term for "source
// crawler") rather than /admin/trace/sources to keep this rollup
// surface distinct from the live SourceTrace endpoint.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// digestAdminHandler bundles the deps for /admin/trace/seeds/{id}/digest.
// trace is required (Postgres path); iceberg may be nil — when the
// requested date is older than Postgres retention and no catalog is
// wired the endpoint returns 503 so the operator UI can show a clear
// "configure ICEBERG_CATALOG_URI" message instead of an empty body.
type digestAdminHandler struct {
	trace   *repository.TraceRepository
	iceberg *icebergclient.Catalog
}

// registerDigestAdmin wires the endpoint on the supplied mux. Called
// from registerSourcesAdmin adjacent to registerTraceAdmin so both
// historic + live trace surfaces ship together.
func registerDigestAdmin(mux *http.ServeMux, trace *repository.TraceRepository, iceberg *icebergclient.Catalog) {
	h := digestAdminHandler{trace: trace, iceberg: iceberg}
	mux.HandleFunc("GET /admin/trace/seeds/{id}/digest", requireAdmin(h.Digest))
}

// Digest returns a per-day rollup for a source. The data_source field
// flags which backing store served the answer:
//
//   - "postgres" — date is within the 7d retention window.
//   - "iceberg"  — date is older; counts come from the Iceberg sinks.
//
// Postgres path: a single SELECT with three sub-counts. Iceberg path:
// reads three tables (variants_ingested via opportunities.variants,
// opportunities.published, opportunities.variants_rejected) and
// filters client-side. The iceberg path is bounded (limit 5000 per
// table); for a single source on a single day that's safe headroom
// today — Plan D can move to partition-pruned reads if it ever isn't.
func (h digestAdminHandler) Digest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sourceID := r.PathValue("id")
	if sourceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "source id required")
		return
	}
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		dateStr = time.Now().UTC().Format("2006-01-02")
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "date must be YYYY-MM-DD")
		return
	}

	dayStart := date.UTC()
	dayEnd := dayStart.Add(24 * time.Hour)

	// Retention threshold: anything older than (now - 7d) is past the
	// pipeline_variants retention. Cutoff is the start of the date so
	// a day that spans the boundary still uses the Iceberg path
	// (consistent with how SourceTrace decides).
	usePostgres := !dayStart.Before(time.Now().Add(-7 * 24 * time.Hour))

	out := map[string]any{
		"source_id": sourceID,
		"date":      dateStr,
	}

	if usePostgres {
		summary, derr := h.trace.DayDigest(ctx, sourceID, dayStart, dayEnd)
		if derr != nil {
			writeError(w, http.StatusInternalServerError, "internal", derr.Error())
			return
		}
		out["data_source"] = "postgres"
		out["crawl_jobs"] = summary.CrawlJobs
		out["variants_emitted"] = summary.VariantsEmitted
		out["variants_rejected"] = summary.VariantsRejected
		out["variants_published"] = summary.VariantsPublished
		out["rejection_reasons"] = summary.RejectionReasons
		writeJSON(w, http.StatusOK, out)
		return
	}

	if h.iceberg == nil {
		writeError(w, http.StatusServiceUnavailable, "iceberg_unavailable",
			"Iceberg catalog not configured for historic queries")
		return
	}

	emitted, published, rejected, reasons := h.icebergDigest(ctx, sourceID, dayStart, dayEnd)
	out["data_source"] = "iceberg"
	out["crawl_jobs"] = int64(0) // crawl_jobs isn't yet a writer-persisted Iceberg table
	out["variants_emitted"] = emitted
	out["variants_published"] = published
	out["variants_rejected"] = rejected
	out["rejection_reasons"] = reasons
	writeJSON(w, http.StatusOK, out)
}

// icebergDigest scans the three writer-persisted variant sinks for the
// requested source/day window and returns (emitted, published, rejected,
// reasons). Soft-fails: individual table errors log and contribute 0 so
// the operator still sees what data was available.
func (h digestAdminHandler) icebergDigest(
	ctx context.Context,
	sourceID string,
	dayStart, dayEnd time.Time,
) (emitted, published, rejected int64, reasons map[string]int64) {
	reasons = map[string]int64{}

	if ingested, err := h.iceberg.ReadRecent(ctx, "opportunities", "variants",
		[]string{"source_id", "scraped_at"}, 5000); err == nil {
		emitted = countInWindow(ingested, "scraped_at", sourceID, dayStart, dayEnd)
	}
	if pubRows, err := h.iceberg.ReadRecent(ctx, "opportunities", "published",
		[]string{"source_id", "scraped_at"}, 5000); err == nil {
		published = countInWindow(pubRows, "scraped_at", sourceID, dayStart, dayEnd)
	}
	if rejRows, err := h.iceberg.ReadRecent(ctx, "opportunities", "variants_rejected",
		[]string{"source_id", "reasons", "rejected_at"}, 5000); err == nil {
		for _, row := range rejRows {
			if !rowSourceMatches(row["source_id"], sourceID) {
				continue
			}
			t, ok := parseRowTimestamp(row["rejected_at"])
			if !ok || t.Before(dayStart) || !t.Before(dayEnd) {
				continue
			}
			rejected++
			for _, reason := range parseRejectionReasons(row["reasons"]) {
				reasons[reason]++
			}
		}
	}
	return emitted, published, rejected, reasons
}

// countInWindow filters Iceberg-marshaled rows on (source_id, [start,
// end)) using timeColumn for the time check. Shared by the digest
// emitted/published counters.
func countInWindow(rows []map[string]any, timeColumn, sourceID string, start, end time.Time) int64 {
	var n int64
	for _, row := range rows {
		if !rowSourceMatches(row["source_id"], sourceID) {
			continue
		}
		t, ok := parseRowTimestamp(row[timeColumn])
		if !ok || t.Before(start) || !t.Before(end) {
			continue
		}
		n++
	}
	return n
}
