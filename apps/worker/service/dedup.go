package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
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
//
// When skipCache is true the handler bypasses both KV lookups and
// derives cluster_id deterministically from HardKey. Used as a
// throughput escape hatch when JetStream-KV is slow enough to stall
// the canonical chain (each KV op blocks ~5s on timeout). Loses
// cross-source dedup until flipped back.
type DedupHandler struct {
	svc          *frame.Service
	cache        cache.Cache[string, string]
	clusterCache cache.Cache[string, kv.ClusterSnapshot]
	skipCache    bool
	store        *variantstate.Store // nil-safe; soft-fails on Postgres outage
	// readBackend controls hard_key → cluster_id lookup order:
	//   "valkey" (default) — Valkey first, Postgres fallback (Phase 4a)
	//   "postgres"         — Postgres first, Valkey fallback (Phase 4b)
	//   "postgres-only"    — Postgres only, no Valkey reads or writes (4c)
	// Any other value, including "", is treated as "valkey".
	readBackend string
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

// NewDedupHandlerWithSkip returns a handler with cache bypass toggled,
// defaulting to the Valkey-primary read order. Retained for tests that
// don't exercise the Postgres-cutover flag.
func NewDedupHandlerWithSkip(svc *frame.Service, c cache.Cache[string, string], cs cache.Cache[string, kv.ClusterSnapshot], skipCache bool, store *variantstate.Store) *DedupHandler {
	return NewDedupHandlerWithBackend(svc, c, cs, skipCache, store, "valkey")
}

// NewDedupHandlerWithBackend wires the read-order cutover flag. See
// DedupHandler.readBackend for the legal values.
func NewDedupHandlerWithBackend(svc *frame.Service, c cache.Cache[string, string], cs cache.Cache[string, kv.ClusterSnapshot], skipCache bool, store *variantstate.Store, readBackend string) *DedupHandler {
	return &DedupHandler{
		svc:          svc,
		cache:        c,
		clusterCache: cs,
		skipCache:    skipCache,
		store:        store,
		readBackend:  readBackend,
	}
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

	// skipCache short-circuits both KV lookups and uses HardKey as
	// the cluster_id. Used when KV operations are timing out under
	// load and the chain needs to drain. Cross-source dedup is lost
	// (the same job at two sources clusters separately) until the
	// flag flips back.
	if h.skipCache {
		canonicalID := val.HardKey
		out := eventsv1.VariantClusteredV1{
			VariantID:     val.VariantID,
			OpportunityID: canonicalID,
			HardKey:       val.HardKey,
			Kind:          val.Kind,
			IsNew:         true,
			ClusteredAt:   time.Now().UTC(),
			Attributes:    val.Attributes,
		}
		outEnv := eventsv1.NewEnvelope(eventsv1.TopicVariantsClustered, out)
		if err := h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsClustered, outEnv); err != nil {
			return err
		}
		_ = h.store.AdvanceStage(ctx, val.VariantID,
			variantstate.StageValidated, variantstate.StageClustered,
			&canonicalID, nil)
		return nil
	}

	// Hard-key resolution. The read order is controlled by
	// h.readBackend (Phase 4 cutover flag); writes go to both stores
	// until "postgres-only" is set.
	//
	// Errors at either store fall back to a "miss" so the canonical
	// chain never stalls on a degraded backend — same soft-fail
	// philosophy as Phase 3's variantstate writes.
	clusterID, hit := h.lookupHardKey(ctx, val.HardKey)
	isNew := false
	if !hit {
		clusterID = xid.New().String()
		isNew = true
	}
	// Writes (additive; AdvanceStage below records the canonical_id in
	// pipeline_variants on every emit, so Postgres always sees the
	// mapping even when we read Valkey-first).
	if h.readBackend != "postgres-only" {
		if err := h.cache.Set(ctx, val.HardKey, clusterID, 0*time.Second); err != nil {
			util.Log(ctx).WithError(err).
				WithField("hard_key", val.HardKey).
				Warn("dedup: cache.Set failed; cluster mapping lost for next variant")
		}
	}

	// Seed/refresh the cluster snapshot's Attributes (and Kind) so
	// the canonical-merge stage finds them on its read. We merge —
	// newer keys overwrite older ones, but pre-existing keys not
	// present in the new variant survive.
	if h.clusterCache != nil {
		prev, _, gerr := h.clusterCache.Get(ctx, clusterID)
		if gerr != nil {
			util.Log(ctx).WithError(gerr).
				WithField("cluster_id", clusterID).
				Warn("dedup: clusterCache.Get failed; starting from empty snapshot")
			prev = kv.ClusterSnapshot{}
		}
		merged := mergeAttributes(prev.Attributes, val.Attributes)
		prev.ClusterID = clusterID
		prev.Kind = val.Kind
		prev.Attributes = merged
		// Track the source — last-write-wins for clusters that span
		// multiple sources, since /admin/sources/stop only needs to
		// hit the most recent contributing source to remove a
		// canonical from search. (A future per-source partial-delete
		// would need a multi-source set here.)
		if val.SourceID != "" {
			prev.SourceID = val.SourceID
		}
		if err := h.clusterCache.Set(ctx, clusterID, prev, 0*time.Second); err != nil {
			util.Log(ctx).WithError(err).
				WithField("cluster_id", clusterID).
				Warn("dedup: clusterCache.Set failed; canonical merge will start from empty snapshot")
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
	if err := h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsClustered, outEnv); err != nil {
		return err
	}
	_ = h.store.AdvanceStage(ctx, val.VariantID,
		variantstate.StageValidated, variantstate.StageClustered,
		&clusterID, nil)
	return nil
}

// lookupHardKey resolves a hard_key to its cluster_id (canonical_id),
// honouring the Phase 4 readBackend cutover flag.
//
// Returns (clusterID, true) on hit, ("", false) on miss in both
// stores. A backend-specific error is treated as a clean miss after a
// WARN log so the chain proceeds without nacking.
func (h *DedupHandler) lookupHardKey(ctx context.Context, hardKey string) (string, bool) {
	postgresFirst := h.readBackend == "postgres" || h.readBackend == "postgres-only"
	noValkey := h.readBackend == "postgres-only"

	tryPostgres := func() (string, bool) {
		cid, err := h.store.LookupCanonical(ctx, hardKey)
		if err != nil {
			// LookupCanonical already soft-fails internally; this
			// branch is here for future strict-error variants.
			util.Log(ctx).WithError(err).
				WithField("hard_key", hardKey).
				Warn("dedup: postgres lookup failed; treating as miss")
			return "", false
		}
		if cid == nil || *cid == "" {
			return "", false
		}
		return *cid, true
	}

	tryValkey := func() (string, bool) {
		v, hit, err := h.cache.Get(ctx, hardKey)
		if err != nil {
			util.Log(ctx).WithError(err).
				WithField("hard_key", hardKey).
				Warn("dedup: valkey lookup failed; treating as miss")
			return "", false
		}
		if !hit {
			return "", false
		}
		return v, true
	}

	if postgresFirst {
		if cid, ok := tryPostgres(); ok {
			return cid, true
		}
		if noValkey {
			return "", false
		}
		return tryValkey()
	}
	// Valkey-first (default, Phase 4a). On miss, the Postgres fallback
	// surfaces cross-source dedup entries written by an earlier
	// AdvanceStage from another worker pod.
	if cid, ok := tryValkey(); ok {
		return cid, true
	}
	return tryPostgres()
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
