package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
)

// CanonicalHandler merges a newly-clustered variant into the
// current cluster snapshot (or creates one) and emits
// CanonicalUpsertedV1.
//
// TODO(opportunity-generification): this handler is structurally
// stale after the polymorphic event reshape. The merge logic still
// references the legacy ClusterSnapshot fields — Phase 3.3 will
// rewrite the snapshot store to track Attributes per kind, and
// Phase 4.x will rewire the canonical-merge stage to read Validated +
// Normalized from the new event shape (currently the validated and
// normalized payloads no longer embed each other). Until then, the
// canonical merge keeps the cluster-snapshot model untouched and
// produces an opportunity-shaped event with placeholder values for
// kind-specific fields. Tests that exercise the full pipeline will
// need to wait for Phase 3.3.
type CanonicalHandler struct {
	svc   *frame.Service
	cache cache.Cache[string, kv.ClusterSnapshot]
}

// NewCanonicalHandler binds the handler.
func NewCanonicalHandler(svc *frame.Service, c cache.Cache[string, kv.ClusterSnapshot]) *CanonicalHandler {
	return &CanonicalHandler{svc: svc, cache: c}
}

// Name ...
func (h *CanonicalHandler) Name() string { return eventsv1.TopicVariantsClustered }

// PayloadType ...
func (h *CanonicalHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *CanonicalHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("canonical: empty payload")
	}
	return nil
}

// Execute merges and emits.
func (h *CanonicalHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.VariantClusteredV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	in := env.Payload

	// TODO(opportunity-generification): VariantClusteredV1 no longer
	// embeds the validated/normalized payload. Phase 3.3 will rewrite
	// dedup to populate Attributes on the merged snapshot. For now we
	// load the existing snapshot keyed by OpportunityID (the new
	// cluster identity) and re-emit it; merge-from-new-variant work is
	// deferred.
	prev, _, err := h.cache.Get(ctx, in.OpportunityID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	merged := kv.ClusterSnapshot{
		ClusterID:    in.OpportunityID,
		CanonicalID:  prev.CanonicalID,
		Slug:         prev.Slug,
		Title:        prev.Title,
		Company:      prev.Company,
		Description:  prev.Description,
		Country:      prev.Country,
		Language:     prev.Language,
		RemoteType:   prev.RemoteType,
		Status:       "active",
		LastSeenAt:   now,
		PostedAt:     prev.PostedAt,
		ApplyURL:     prev.ApplyURL,
		QualityScore: prev.QualityScore,
	}
	if merged.FirstSeenAt.IsZero() {
		merged.FirstSeenAt = now
	} else {
		merged.FirstSeenAt = prev.FirstSeenAt
	}
	if merged.CanonicalID == "" {
		merged.CanonicalID = xid.New().String()
	}
	if merged.Slug == "" {
		// Build a human-readable slug from kind + title + issuer + a short ID suffix.
		hashSuffix := merged.CanonicalID
		if len(hashSuffix) > 8 {
			hashSuffix = hashSuffix[:8]
		}
		merged.Slug = domain.BuildSlug(in.Kind, merged.Title, merged.Company, hashSuffix)
	}

	if err := h.cache.Set(ctx, merged.ClusterID, merged, 0*time.Second); err != nil {
		return err
	}

	out := eventsv1.CanonicalUpsertedV1{
		OpportunityID: merged.CanonicalID,
		Slug:          merged.Slug,
		HardKey:       in.HardKey,
		Kind:          in.Kind,
		Title:         merged.Title,
		IssuingEntity: merged.Company,
		ApplyURL:      merged.ApplyURL,
		AnchorCountry: merged.Country,
		Remote:        merged.RemoteType == "remote",
		PostedAt:      merged.PostedAt,
		Currency:      merged.Currency,
		AmountMin:     merged.SalaryMin,
		AmountMax:     merged.SalaryMax,
		UpsertedAt:    now,
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsUpserted, out)
	return h.svc.EventsManager().Emit(ctx, eventsv1.TopicCanonicalsUpserted, outEnv)
}

func preferNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func preferLonger(a, b string) string {
	if len(a) > len(b) {
		return a
	}
	return b
}

func preferNonZero(a, b float64) float64 {
	if a > 0 {
		return a
	}
	return b
}
