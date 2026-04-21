package service

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"
)

// qualityResetter is the narrow interface required by QualityResetHandler.
type qualityResetter interface {
	ResetQualityWindowAll(ctx context.Context) (int64, error)
}

// healthDecayer is the narrow interface required by HealthDecayHandler.
type healthDecayer interface {
	DecayHealth(ctx context.Context, step float64) (int64, error)
}

// QualityResetHandler zeros the rolling quality-window counters on every active source.
func QualityResetHandler(repo qualityResetter) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		n, err := repo.ResetQualityWindowAll(ctx)
		if err != nil {
			util.Log(ctx).WithError(err).Error("quality-reset failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "updated": n})
	}
}

// HealthDecayHandler nudges every active source's health_score toward 1.0.
func HealthDecayHandler(repo healthDecayer) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		n, err := repo.DecayHealth(ctx, 0.05)
		if err != nil {
			util.Log(ctx).WithError(err).Error("health-decay failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "updated": n})
	}
}
