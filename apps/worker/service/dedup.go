package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/rs/xid"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
)

// DedupHandler consumes VariantValidatedV1, looks up the hard_key
// in the Frame-managed dedup cache; if missing, allocates a new
// cluster_id. Emits VariantClusteredV1 either way so canonical
// merge downstream always runs.
//
// Dedup also seeds (or refreshes) the cluster snapshot's Attributes
// map so the canonical-merge stage can read the latest per-kind
// fields without re-reading the validated payload. The merge stage
// then emits CanonicalUpsertedV1.Attributes for the materializer's
// sparseColsForKind to project per-kind facets.
type DedupHandler struct {
	svc          *frame.Service
	cache        cache.Cache[string, string]
	clusterCache cache.Cache[string, kv.ClusterSnapshot]
}

// NewDedupHandler binds the handler. clusterCache may be nil in
// tests that exercise dedup in isolation; when present, dedup
// merges the inbound Attributes into the cluster snapshot so the
// canonical-merge handler always reads a populated map.
func NewDedupHandler(svc *frame.Service, c cache.Cache[string, string]) *DedupHandler {
	return &DedupHandler{svc: svc, cache: c}
}

// NewDedupHandlerWithCluster wires both the dedup cache and the
// cluster-snapshot cache so dedup can persist Attributes ahead of
// canonical merge.
func NewDedupHandlerWithCluster(svc *frame.Service, c cache.Cache[string, string], cs cache.Cache[string, kv.ClusterSnapshot]) *DedupHandler {
	return &DedupHandler{svc: svc, cache: c, clusterCache: cs}
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

	clusterID, hit, err := h.cache.Get(ctx, val.HardKey)
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
		if err := h.cache.Set(ctx, val.HardKey, clusterID, 0*time.Second); err != nil {
			return err
		}
	}

	// Seed/refresh the cluster snapshot's Attributes (and Kind) so
	// the canonical-merge stage finds them on its read. We merge —
	// newer keys overwrite older ones, but pre-existing keys not
	// present in the new variant survive.
	if h.clusterCache != nil {
		prev, _, gerr := h.clusterCache.Get(ctx, clusterID)
		if gerr != nil {
			return gerr
		}
		merged := mergeAttributes(prev.Attributes, val.Attributes)
		prev.ClusterID = clusterID
		prev.Kind = val.Kind
		prev.Attributes = merged
		if err := h.clusterCache.Set(ctx, clusterID, prev, 0*time.Second); err != nil {
			return err
		}
	}

	out := eventsv1.VariantClusteredV1{
		VariantID:     val.VariantID,
		OpportunityID: clusterID,
		HardKey:       val.HardKey,
		Kind:          val.Kind,
		IsNew:         isNew,
		ClusteredAt:   time.Now().UTC(),
		Attributes:    val.Attributes,
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicVariantsClustered, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsClustered, outEnv)
}

// mergeAttributes returns a fresh map containing prev keys overlaid
// with next keys. Either input may be nil; the result is non-nil
// when either is non-empty.
func mergeAttributes(prev, next map[string]any) map[string]any {
	if len(prev) == 0 && len(next) == 0 {
		return nil
	}
	out := make(map[string]any, len(prev)+len(next))
	for k, v := range prev {
		out[k] = v
	}
	for k, v := range next {
		// Skip nil and empty-string overwrites so a sparse update
		// can't blank an established field.
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		out[k] = v
	}
	return out
}
