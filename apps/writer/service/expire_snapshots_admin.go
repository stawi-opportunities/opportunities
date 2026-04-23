package service

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/util"
)

// expireSnapshotsMu guards ExpireSnapshotsHandler against concurrent
// Trustage fires. A second request arriving while a previous run is
// still in flight is rejected with 409 Conflict.
var expireSnapshotsMu sync.Mutex

// ExpireSnapshotsHandler returns a POST handler that runs the nightly
// expiry against AppendOnlyTables via the given catalog.
// Body is ignored; config is taken from env at service startup.
//
// Concurrent requests: if an expire-snapshots run is already in
// progress the handler returns HTTP 409 Conflict immediately.
func ExpireSnapshotsHandler(cat catalog.Catalog, cfg ExpireSnapshotsConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !expireSnapshotsMu.TryLock() {
			util.Log(req.Context()).Warn("expire_snapshots: already in progress, rejecting concurrent request")
			http.Error(w, `{"error":"expire_snapshots already in progress"}`, http.StatusConflict)
			return
		}
		defer expireSnapshotsMu.Unlock()

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
