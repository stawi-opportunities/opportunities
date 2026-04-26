package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/pitabwire/frame"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// NormalizeHandler consumes VariantIngestedV1, applies deterministic
// normalization (country codes, remote-type inference), and emits
// VariantNormalizedV1.
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

// normalize applies deterministic field cleanup. Rules mirror the
// legacy pkg/pipeline/handlers/normalize.go but without the Postgres
// load — we operate purely on the event payload.
func normalize(in eventsv1.VariantIngestedV1) eventsv1.VariantNormalizedV1 {
	out := eventsv1.VariantNormalizedV1{
		VariantID:      in.VariantID,
		SourceID:       in.SourceID,
		ExternalID:     in.ExternalID,
		HardKey:        in.HardKey,
		Stage:          "normalized",
		Title:          strings.TrimSpace(in.Title),
		Company:        strings.TrimSpace(in.Company),
		LocationText:   strings.TrimSpace(in.LocationText),
		Country:        strings.ToUpper(strings.TrimSpace(in.Country)),
		Language:       strings.ToLower(strings.TrimSpace(in.Language)),
		RemoteType:     inferRemoteType(in.RemoteType, in.LocationText),
		EmploymentType: strings.ToLower(strings.TrimSpace(in.EmploymentType)),
		SalaryMin:      in.SalaryMin,
		SalaryMax:      in.SalaryMax,
		Currency:       strings.ToUpper(strings.TrimSpace(in.Currency)),
		Description:    strings.TrimSpace(in.Description),
		ApplyURL:       strings.TrimSpace(in.ApplyURL),
		PostedAt:       in.PostedAt,
		ScrapedAt:      in.ScrapedAt,
		ContentHash:    in.ContentHash,
		RawArchiveRef:  in.RawArchiveRef,
	}
	return out
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
