package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/pitabwire/frame"
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
}

// NewNormalizeHandler binds a handler to the Frame service for
// re-emitting events.
func NewNormalizeHandler(svc *frame.Service, store *variantstate.Store) *NormalizeHandler {
	return &NormalizeHandler{svc: svc, store: store}
}

// Name is the topic this handler consumes.
func (h *NormalizeHandler) Name() string { return eventsv1.TopicVariantsIngested }

// PayloadType returns a pointer for Frame's JSON deserializer. We
// unwrap the full envelope ourselves.
func (h *NormalizeHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate accepts any non-empty JSON payload — type-check is done
// in Execute against the envelope.
func (h *NormalizeHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("normalize: empty or wrong payload")
	}
	return nil
}

// Execute parses the envelope, normalizes, and emits.
func (h *NormalizeHandler) Execute(ctx context.Context, payload any) error {
	t0 := time.Now()
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		util.Log(ctx).WithError(err).Warn("normalize: unmarshal failed")
		return err
	}

	// (Defensive ledger upsert removed.) The worker reads pooler-rw so
	// there's no replication lag between the crawler's commit and the
	// worker's AdvanceStage. With the TimescaleDB hypertable PK now
	// being composite (variant_id, ingested_at), a defensive INSERT
	// with a fresh ingested_at would create a second row for the same
	// variant_id rather than no-op'ing — so the safer move is to let
	// AdvanceStage fall through as a no-op when the row is genuinely
	// absent.
	t1 := time.Now()
	out := normalize(env.Payload)
	t2 := time.Now()
	// Hop 1 of the Events→Queue migration. PRIMARY (chain): hand
	// VariantNormalizedV1 to the validate stage over a dedicated, durable
	// Frame Queue subject — a per-consumer durable subscriber, no shared-bus
	// cross-talk. TRANSITIONAL: still Emit on the events bus so the writer's
	// Iceberg archival (events-based until its own cutover) keeps receiving
	// normalized; that Emit (and the resulting normalized.v1 catch-all noise)
	// goes away in the final writer-cutover hop. The ledger advance gates on
	// the queue publish (the load-bearing path).
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicVariantsNormalized, out)
	body, err := json.Marshal(outEnv)
	if err == nil {
		err = h.svc.QueueManager().Publish(ctx, eventsv1.SubjectPipelineNormalized, body)
	}
	if emitErr := h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsNormalized, outEnv); emitErr != nil {
		util.Log(ctx).WithError(emitErr).Warn("normalize: transitional events emit failed; queue path is authoritative")
	}
	t3 := time.Now()

	// Ledger advance — only on successful emit. CAS guards against
	// double-advance on redelivery.
	if err == nil {
		_ = h.store.AdvanceStage(ctx, env.Payload.VariantID,
			variantstate.StageIngested, variantstate.StageNormalized,
			nil, nil)
	}

	util.Log(ctx).
		WithField("variant_id", env.Payload.VariantID).
		WithField("payload_bytes", len(*raw)).
		WithField("unmarshal_ms", t1.Sub(t0).Milliseconds()).
		WithField("normalize_ms", t2.Sub(t1).Milliseconds()).
		WithField("emit_ms", t3.Sub(t2).Milliseconds()).
		WithField("total_ms", t3.Sub(t0).Milliseconds()).
		Info("normalize: done")
	return err
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
