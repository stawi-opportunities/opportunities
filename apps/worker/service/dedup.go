package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/rs/xid"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// DedupHandler consumes VariantValidatedV1, looks up the hard_key
// in the Frame-managed dedup cache; if missing, allocates a new
// cluster_id. Emits VariantClusteredV1 either way so canonical
// merge downstream always runs.
type DedupHandler struct {
	svc   *frame.Service
	cache cache.Cache[string, string]
}

// NewDedupHandler binds the handler.
func NewDedupHandler(svc *frame.Service, c cache.Cache[string, string]) *DedupHandler {
	return &DedupHandler{svc: svc, cache: c}
}

// Name ...
func (h *DedupHandler) Name() string { return eventsv1.TopicVariantsValidated }

// PayloadType ...
func (h *DedupHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *DedupHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("dedup: empty payload")
	}
	return nil
}

// Execute dedups and emits.
func (h *DedupHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantValidatedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	val := env.Payload

	clusterID, hit, err := h.cache.Get(ctx, val.Normalized.HardKey)
	if err != nil {
		return err
	}
	isNew := false
	if !hit {
		clusterID = xid.New().String()
		isNew = true
		// TTL 0 means "no expiry" for Frame caches that honour it;
		// backends that don't get the framework default. Dedup
		// assignments are permanent.
		if err := h.cache.Set(ctx, val.Normalized.HardKey, clusterID, 0*time.Second); err != nil {
			return err
		}
	}

	out := eventsv1.VariantClusteredV1{
		VariantID: val.VariantID,
		ClusterID: clusterID,
		IsNew:     isNew,
		Validated: val,
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicVariantsClustered, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsClustered, outEnv)
}
