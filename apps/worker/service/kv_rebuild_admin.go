package service

import (
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"
)

// KVRebuildHandler returns an http.HandlerFunc that triggers a Valkey
// cluster:* rebuild from R2 canonicals_current/. The response body is
// a JSON-encoded KVRebuildResult with row and key counts.
func KVRebuildHandler(r *KVRebuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		res, err := r.Run(ctx)
		if err != nil {
			util.Log(ctx).WithError(err).Error("kv rebuild failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}
