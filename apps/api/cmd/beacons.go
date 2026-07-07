// apps/api/cmd/beacons.go
//
// View / apply beacons + the public stats endpoint that surfaces the
// Valkey-backed counters. Counter writes are best-effort: the analytics
// log to OpenObserve is always shipped (even when Valkey is unavailable)
// so analytics queries have an authoritative source even when the read
// path counters happen to lag.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/analytics"
	"github.com/stawi-opportunities/opportunities/pkg/counters"
)

// corsBeaconPreflight is the shared OPTIONS handler for the
// `sendBeacon`-friendly POST endpoints. Same headers as the original
// view beacon — kept in one place so the view + apply paths can't
// drift.
func corsBeaconPreflight(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "3600")
	w.WriteHeader(http.StatusNoContent)
}

// viewBeaconHandler is POST /jobs/{slug}/view. Increments the Valkey
// view counters (best-effort) and ships an analytics event to
// OpenObserve. Returns 204 unconditionally — the beacon contract is
// fire-and-forget.
func viewBeaconHandler(an *analytics.Client, ct *counters.Counters) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		slug := req.PathValue("slug")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if slug == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if an != nil {
			an.Send(req.Context(), "opportunities_views", buildBeaconEvent(req, slug, "server_view"))
		}
		incrCounterAsync(req.Context(), ct, "view", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// applyBeaconHandler is POST /opportunities/{slug}/apply. Mirror of
// viewBeaconHandler: counter + OpenObserve, both best-effort.
func applyBeaconHandler(an *analytics.Client, ct *counters.Counters) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		slug := req.PathValue("slug")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if slug == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if an != nil {
			an.Send(req.Context(), "opportunity_applies", buildBeaconEvent(req, slug, "server_apply"))
		}
		incrCounterAsync(req.Context(), ct, "apply", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// buildBeaconEvent constructs the canonical event shape both view and
// apply beacons ship. Keeping the event-shape identical between the
// two streams means the OpenObserve dashboard can use one query
// template across both — only the stream name changes.
func buildBeaconEvent(req *http.Request, slug, eventName string) map[string]any {
	evt := map[string]any{
		"event":      eventName,
		"slug":       slug,
		"ip_hash":    hashIP(req),
		"user_agent": req.Header.Get("User-Agent"),
		"referer":    req.Header.Get("Referer"),
		"cf_country": req.Header.Get("CF-IPCountry"),
		"cf_ray":     req.Header.Get("CF-Ray"),
	}
	if profileID := profileIDFromJWT(req); profileID != "" {
		evt["profile_id"] = profileID
	}
	return evt
}

// incrCounterAsync runs the Valkey INCR + EXPIRE pipeline on a fresh
// context (the request context can be cancelled by the time the
// caller has written its 204). Errors are logged at debug — the
// OpenObserve write is the source of truth for analytics.
func incrCounterAsync(reqCtx context.Context, ct *counters.Counters, kind, slug string) {
	if ct == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var err error
		switch kind {
		case "view":
			err = ct.IncrView(ctx, slug)
		case "apply":
			err = ct.IncrApply(ctx, slug)
		}
		if err != nil {
			util.Log(reqCtx).WithError(err).
				WithField("slug", slug).
				WithField("kind", kind).
				Debug("counter incr failed")
		}
	}()
}

// statsHandler is GET /opportunities/{slug}/stats. Returns the four
// counters; missing keys count as zero. When ct is nil the response
// is the well-formed all-zero shape so callers can render a "no data
// yet" state without branching on a 503.
func statsHandler(ct *counters.Counters) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		slug := req.PathValue("slug")
		w.Header().Set("Cache-Control", "public, max-age=15")
		w.Header().Set("Content-Type", "application/json")
		if slug == "" {
			http.Error(w, `{"error":"slug required"}`, http.StatusBadRequest)
			return
		}
		stats := counters.Stats{}
		if ct != nil {
			s, err := ct.GetStats(req.Context(), slug)
			if err != nil {
				util.Log(req.Context()).WithError(err).
					WithField("slug", slug).
					Warn("stats lookup failed")
			} else {
				stats = s
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"slug":          slug,
			"views_total":   stats.ViewsTotal,
			"views_24h":     stats.Views24h,
			"applies_total": stats.AppliesTotal,
			"applies_24h":   stats.Applies24h,
		})
	}
}
