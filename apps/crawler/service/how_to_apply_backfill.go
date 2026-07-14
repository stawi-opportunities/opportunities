package service

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/howtoapply"
)

// HowToApplyBackfillDeps wires POST /admin/how-to-apply/backfill.
// Peeler is *extraction.Extractor pointed at NVIDIA Build in production:
//
//	INFERENCE_BASE_URL=https://integrate.api.nvidia.com
//	INFERENCE_MODEL=meta/llama-3.1-8b-instruct
//	INFERENCE_API_KEY from Vault nvidia_api_key
type HowToApplyBackfillDeps struct {
	DB     *sql.DB
	Peeler howtoapply.Peeler
	// Limit is the default max rows per tick when the request omits limit.
	Limit int
}

// HowToApplyBackfillHandler peels a batch of opportunity descriptions via
// the shared inference client (NVIDIA Build). Trustage fires this on a
// schedule so historical rows get how_to_apply without a one-shot CLI.
//
//	POST /admin/how-to-apply/backfill?limit=25&force=false
//	→ 200 { scanned, peeled, skipped, failed }
//	→ 503 when INFERENCE_* is unset
func HowToApplyBackfillHandler(deps HowToApplyBackfillDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		if deps.DB == nil {
			http.Error(w, `{"error":"database not configured"}`, http.StatusBadGateway)
			return
		}
		if deps.Peeler == nil {
			http.Error(w, `{"error":"inference not configured (set INFERENCE_* / NVIDIA Build)"}`, http.StatusServiceUnavailable)
			return
		}
		opts := howtoapply.BackfillOptions{
			Limit:  deps.Limit,
			Force:  r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1",
			DryRun: r.URL.Query().Get("dry_run") == "true",
		}
		if opts.Limit <= 0 {
			opts.Limit = 25
		}
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
				opts.Limit = n
			}
		}
		res, err := howtoapply.Run(ctx, deps.DB, deps.Peeler, opts)
		if err != nil {
			log.WithError(err).Error("how-to-apply backfill failed")
			http.Error(w, `{"error":"backfill failed"}`, http.StatusBadGateway)
			return
		}
		log.WithField("scanned", res.Scanned).WithField("peeled", res.Peeled).
			WithField("skipped", res.Skipped).WithField("failed", res.Failed).
			Info("how-to-apply backfill tick")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}
