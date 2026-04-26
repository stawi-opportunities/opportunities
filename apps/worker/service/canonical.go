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

// CanonicalHandler merges a newly-clustered variant into the
// current cluster snapshot (or creates one) and emits
// CanonicalUpsertedV1.
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
	n := in.Validated.Normalized

	// Load existing snapshot, if any.
	prev, _, err := h.cache.Get(ctx, in.ClusterID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	merged := kv.ClusterSnapshot{
		ClusterID:      in.ClusterID,
		CanonicalID:    prev.CanonicalID,
		Slug:           prev.Slug,
		Title:          preferNonEmpty(n.Title, prev.Title),
		Company:        preferNonEmpty(n.Company, prev.Company),
		Description:    preferLonger(n.Description, prev.Description),
		Country:        preferNonEmpty(n.Country, prev.Country),
		Language:       preferNonEmpty(n.Language, prev.Language),
		RemoteType:     preferNonEmpty(n.RemoteType, prev.RemoteType),
		EmploymentType: preferNonEmpty(n.EmploymentType, prev.EmploymentType),
		SalaryMin:      preferNonZero(n.SalaryMin, prev.SalaryMin),
		SalaryMax:      preferNonZero(n.SalaryMax, prev.SalaryMax),
		Currency:       preferNonEmpty(n.Currency, prev.Currency),
		Category:       prev.Category,
		QualityScore:   prev.QualityScore,
		Status:         "active",
		LastSeenAt:     now,
		PostedAt:       n.PostedAt,
		ApplyURL:       preferNonEmpty(n.ApplyURL, prev.ApplyURL),
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
		merged.Slug = merged.CanonicalID // placeholder — Phase 5 replaces with human-readable slug generator
	}

	if err := h.cache.Set(ctx, merged.ClusterID, merged, 0*time.Second); err != nil {
		return err
	}

	out := eventsv1.CanonicalUpsertedV1{
		CanonicalID:    merged.CanonicalID,
		ClusterID:      merged.ClusterID,
		Slug:           merged.Slug,
		Title:          merged.Title,
		Company:        merged.Company,
		Description:    merged.Description,
		LocationText:   n.LocationText,
		Country:        merged.Country,
		Language:       merged.Language,
		RemoteType:     merged.RemoteType,
		EmploymentType: merged.EmploymentType,
		Seniority:      merged.Seniority,
		SalaryMin:      merged.SalaryMin,
		SalaryMax:      merged.SalaryMax,
		Currency:       merged.Currency,
		Category:       merged.Category,
		QualityScore:   merged.QualityScore,
		Status:         merged.Status,
		PostedAt:       merged.PostedAt,
		FirstSeenAt:    merged.FirstSeenAt,
		LastSeenAt:     merged.LastSeenAt,
		ExpiresAt:      merged.FirstSeenAt.Add(120 * 24 * time.Hour),
		ApplyURL:       merged.ApplyURL,
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
