package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/queue"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// NormalizeHandler consumes VariantIngestedV1, applies deterministic
// normalization (country codes, remote-type inference), and emits
// VariantNormalizedV1.
//
// The Attributes map flows through verbatim; well-known string keys
// (location_text, language, employment_type, …) are trimmed and
// lowercased in place. Universal envelope fields (title,
// issuing_entity, country, currency, amount_min/max) are mirrored
// into Attributes so downstream stages can read everything from a
// single map.
type NormalizeHandler struct {
	svc   *frame.Service
	store *variantstate.Store // nil-safe; soft-fails on Postgres outage
	next  string              // Frame Queue Name to publish VariantNormalizedV1 to
}

// NewNormalizeHandler binds the handler. next is the Queue Name of the
// downstream (normalized) queue this stage publishes to.
func NewNormalizeHandler(svc *frame.Service, store *variantstate.Store, next string) *NormalizeHandler {
	return &NormalizeHandler{svc: svc, store: store, next: next}
}

var _ queue.SubscribeWorker = (*NormalizeHandler)(nil)

// Handle consumes a VariantIngestedV1 envelope, normalizes it, and
// publishes VariantNormalizedV1 to the next pipeline queue. Returning a
// non-nil error makes Frame redeliver (durable retry).
func (h *NormalizeHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	t0 := time.Now()
	var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		util.Log(ctx).WithError(err).Warn("normalize: unmarshal failed")
		return err
	}

	out := normalize(env.Payload)
	body, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsNormalized, out))
	if err != nil {
		return err
	}
	if err := h.svc.QueueManager().Publish(ctx, h.next, body, nil); err != nil {
		return err
	}

	// Ledger advance — only after a successful publish. CAS guards against
	// double-advance on redelivery.
	_ = h.store.AdvanceStage(ctx, env.Payload.VariantID,
		variantstate.StageIngested, variantstate.StageNormalized, nil, nil)

	util.Log(ctx).
		WithField("variant_id", env.Payload.VariantID).
		WithField("total_ms", time.Since(t0).Milliseconds()).
		Info("normalize: done")
	return nil
}

// normalize applies deterministic field cleanup against the universal
// envelope (and pulls a few well-known string Attributes through the
// same trim/lower passes).
func normalize(in eventsv1.VariantIngestedV1) eventsv1.VariantNormalizedV1 {
	attrs := map[string]any{}
	for k, v := range in.Attributes {
		attrs[k] = v
	}
	if s, ok := attrs["location_text"].(string); ok {
		attrs["location_text"] = strings.TrimSpace(s)
	}
	if s, ok := attrs["language"].(string); ok {
		attrs["language"] = strings.ToLower(strings.TrimSpace(s))
	}
	if s, ok := attrs["employment_type"].(string); ok {
		attrs["employment_type"] = strings.ToLower(strings.TrimSpace(s))
	}
	// remote_type inference uses location_text fallback.
	loc, _ := attrs["location_text"].(string)
	rt, _ := attrs["remote_type"].(string)
	attrs["remote_type"] = inferRemoteType(rt, loc)
	// Description is attribute-borne (no universal-envelope field) and is
	// the richest signal for the embedding + the job detail page. Always
	// set the key — even to "" — so every downstream stage (canonical
	// merge, embed) can read it without a fragile presence check that
	// silently degrades to an empty vector input when the key is missing.
	desc, _ := attrs["description"].(string)
	attrs["description"] = strings.TrimSpace(desc)
	if s, ok := attrs["apply_url"].(string); ok {
		attrs["apply_url"] = strings.TrimSpace(s)
	}

	// Universal envelope echo as attributes for downstream simplicity.
	attrs["title"] = strings.TrimSpace(in.Title)
	attrs["issuing_entity"] = strings.TrimSpace(in.IssuingEntity)
	attrs["country"] = strings.ToUpper(strings.TrimSpace(in.AnchorCountry))
	attrs["currency"] = strings.ToUpper(strings.TrimSpace(in.Currency))
	attrs["amount_min"] = in.AmountMin
	attrs["amount_max"] = in.AmountMax

	return eventsv1.VariantNormalizedV1{
		VariantID:    in.VariantID,
		SourceID:     in.SourceID,
		HardKey:      in.HardKey,
		Kind:         in.Kind,
		NormalizedAt: time.Now().UTC(),
		Attributes:   attrs,
	}
}

// inferRemoteType applies the legacy normalize.go heuristic — if the
// location text contains "remote" / "anywhere" and the field wasn't
// explicitly set, mark as remote.
func inferRemoteType(explicit, location string) string {
	if e := strings.ToLower(strings.TrimSpace(explicit)); e != "" {
		return e
	}
	l := strings.ToLower(location)
	switch {
	case strings.Contains(l, "remote"), strings.Contains(l, "anywhere"), strings.Contains(l, "worldwide"):
		return "remote"
	case strings.Contains(l, "hybrid"):
		return "hybrid"
	}
	return ""
}
