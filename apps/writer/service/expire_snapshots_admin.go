package service

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/util"
)

// ExpireSnapshotsHandler returns a POST handler that runs the nightly
// expiry against AppendOnlyTables via the given catalog.
// Body is ignored; config is taken from env at service startup.
func ExpireSnapshotsHandler(cat catalog.Catalog, cfg ExpireSnapshotsConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		start := time.Now()
		res, err := ExpireSnapshots(ctx, cat, cfg)
		dur := time.Since(start)
		if err != nil {
			util.Log(ctx).WithError(err).Error("expire_snapshots: run failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		status := http.StatusOK
		if len(res.FailedTables) > 0 {
			status = http.StatusMultiStatus // 207 — some tables succeeded, some failed
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tables":        res.Tables,
			"expired_total": res.ExpiredTotal,
			"failed_tables": res.FailedTables,
			"duration_ms":   dur.Milliseconds(),
		})
	}
}
