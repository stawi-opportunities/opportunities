package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/pitabwire/frame"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
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
	svc *frame.Service
}

// NewNormalizeHandler binds a handler to the Frame service for
// re-emitting events.
func NewNormalizeHandler(svc *frame.Service) *NormalizeHandler {
	return &NormalizeHandler{svc: svc}
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
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}

	out := normalize(env.Payload)
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicVariantsNormalized, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsNormalized, outEnv)
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
	if s, ok := attrs["description"].(string); ok {
		attrs["description"] = strings.TrimSpace(s)
	}
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
