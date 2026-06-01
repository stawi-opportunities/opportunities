// apps/api/cmd/reparse_admin.go
//
// /admin/raw_payloads/{id}/reparse and /admin/sources/{id}/reparse.
// Both endpoints synthesise a CrawlRequestV1 with RawPayloadID set
// and publish it onto opportunities.crawl.requests.v1. The crawler's
// short-circuit branch (see apps/crawler/service/crawl_request_handler.go
// `reparse`) handles re-extraction without re-fetching the page.
//
// Wired in registerSourcesAdmin alongside the other admin surfaces.
// The same Bearer-token requireAdmin guard is applied per-route.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// reparseCrawlRepo is the narrow CrawlRepository slice the reparse
// handler depends on. Pulled into an interface so tests can swap in
// an in-memory fake without booting Postgres.
type reparseCrawlRepo interface {
	GetRawPayload(ctx context.Context, id string) (*domain.RawPayload, error)
	ListRawPayloadsBySource(ctx context.Context, sourceID string, since time.Duration, limit int) ([]domain.RawPayload, error)
}

// reparseEmitter is the narrow Emit slice — keeps tests honest and
// matches the flagEventEmitter shape used by registerFlagsAdmin.
type reparseEmitter interface {
	Emit(ctx context.Context, topic string, payload any) error
}

// reparseAdmin bundles handler deps.
type reparseAdmin struct {
	crawl   reparseCrawlRepo
	emitter reparseEmitter
}

// registerReparseAdmin wires both POST routes onto mux. Both routes
// require admin via requireAdmin.
func registerReparseAdmin(mux *http.ServeMux, crawl reparseCrawlRepo, emitter reparseEmitter) {
	a := &reparseAdmin{crawl: crawl, emitter: emitter}
	mux.HandleFunc("POST /admin/raw_payloads/{id}/reparse", requireAdmin(a.reparseOne))
	mux.HandleFunc("POST /admin/sources/{id}/reparse", requireAdmin(a.reparseSource))
}

// reparseOne enqueues a single raw_payload for re-extraction.
// Returns 202 + JSON body. The crawler handles the work async; the
// caller polls /admin/trace/* to see whether the re-extraction
// produced a new variant.
func (a *reparseAdmin) reparseOne(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "raw_payload id is required")
		return
	}
	rp, err := a.crawl.GetRawPayload(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if rp == nil {
		writeError(w, http.StatusNotFound, "not_found", "raw_payload not found")
		return
	}
	if err := a.emitReparse(ctx, rp); err != nil {
		writeError(w, http.StatusBadGateway, "emit_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"queued":         1,
		"raw_payload_id": id,
	})
}

// reparseSource enqueues every raw_payload of a source whose fetched_at
// falls inside the supplied window (defaults to 24h). Best-effort —
// individual emit failures are logged + counted as skipped.
func (a *reparseAdmin) reparseSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sourceID := r.PathValue("id")
	if sourceID == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "source id is required")
		return
	}
	since := parseWindow(r.URL.Query().Get("since"), 24*time.Hour)
	rows, err := a.crawl.ListRawPayloadsBySource(ctx, sourceID, since, 1000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	queued := 0
	for i := range rows {
		if err := a.emitReparse(ctx, &rows[i]); err != nil {
			util.Log(ctx).WithError(err).
				WithField("raw_payload_id", rows[i].ID).
				Warn("reparseSource: emit failed; continuing")
			continue
		}
		queued++
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"queued":         queued,
		"source_id":      sourceID,
		"window_seconds": int(since.Seconds()),
	})
}

// emitReparse publishes one CrawlRequestV1 with RawPayloadID set.
// The crawler short-circuit branch picks it up and runs re-extraction.
func (a *reparseAdmin) emitReparse(ctx context.Context, rp *domain.RawPayload) error {
	if a.emitter == nil {
		return errors.New("reparse: emitter unwired")
	}
	payload := eventsv1.CrawlRequestV1{
		RequestID:    xid.New().String(),
		SourceID:     rp.SourceID,
		RawPayloadID: rp.ID,
		ScheduledAt:  time.Now().UTC(),
		Mode:         "reparse",
		Attempt:      1,
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, payload)
	return a.emitter.Emit(ctx, eventsv1.TopicCrawlRequests, env)
}
