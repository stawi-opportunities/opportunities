package service

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/util"
)

// CompactHandler returns a POST handler that runs small-file compaction
// across AppendOnlyTables via the given catalog.
// Body is ignored; config is taken from env at service startup.
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
