package service

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/util"
)

// compactMu guards CompactHandler against concurrent Trustage fires.
// A second request arriving while a previous compact run is still in
// flight is rejected with 409 Conflict rather than allowed to race,
// which would produce OCC conflict storms in the Iceberg catalog.
var compactMu sync.Mutex

// CompactHandler returns a POST handler that runs small-file compaction
// across AppendOnlyTables via the given catalog.
// Body is ignored; config is taken from env at service startup.
//
// Concurrent requests: if a compact run is already in progress, the
// handler returns HTTP 409 Conflict immediately so the caller can back
// off without hammering the catalog.
//
// Response body (200 all-ok, 207 MultiStatus if any table failed):
//
//	{
//	  "tables": 14,
//	  "partitions_processed": 127,
//	  "partitions_skipped": {"already_large": 86, "single_file": 40, "other": 1},
//	  "files_before": 412,
//	  "files_after": 203,
//	  "bytes_rewritten": 8589934592,
//	  "failed_tables": [],
//	  "duration_ms": 412334
//	}
func CompactHandler(cat catalog.Catalog, cfg CompactConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !compactMu.TryLock() {
			util.Log(req.Context()).Warn("compact: already in progress, rejecting concurrent request")
			http.Error(w, `{"error":"compact already in progress"}`, http.StatusConflict)
			return
		}
		defer compactMu.Unlock()

		ctx := req.Context()
		start := time.Now()
		res, err := Compact(ctx, cat, cfg)
		dur := time.Since(start)
		res.DurationMs = dur.Milliseconds()
		if err != nil {
			util.Log(ctx).WithError(err).Error("compact: run failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		status := http.StatusOK
		if len(res.FailedTables) > 0 {
			status = http.StatusMultiStatus // 207 — some tables succeeded, some failed
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(res)
	}
}
