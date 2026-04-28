package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
)

// CanonicalHandler merges a newly-clustered variant into the
// current cluster snapshot (or creates one) and emits
// CanonicalUpsertedV1.
//
// The merge state lives in Valkey via cache.Cache[string,
// kv.ClusterSnapshot]. Attributes flow forward from
// VariantValidatedV1 → VariantClusteredV1 → snapshot, then are
// merged with the snapshot's prior Attributes (newer keys win) and
// re-emitted on CanonicalUpsertedV1. The materializer's
// sparseColsForKind reads the per-kind facets out of that map.
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

	prev, _, err := h.cache.Get(ctx, in.OpportunityID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	// Merge Attributes — dedup already wrote a refreshed map onto
	// the snapshot, but the in-flight VariantClusteredV1 also
	// carries the latest variant's Attributes. Apply both: prev
	// snapshot first, then this variant's keys win.
	mergedAttrs := mergeAttributes(prev.Attributes, in.Attributes)

	// Universal-envelope fields are echoed into Attributes by
	// normalize, so we can read them straight back out for the
	// merged snapshot. preferNonEmpty falls back to the prior
	// snapshot when the current variant is missing the field.
	title := preferNonEmpty(attrStr(mergedAttrs, "title"), prev.Title)
	issuer := preferNonEmpty(attrStr(mergedAttrs, "issuing_entity"), prev.Company)
	country := preferNonEmpty(attrStr(mergedAttrs, "country"), prev.Country)
	currency := preferNonEmpty(attrStr(mergedAttrs, "currency"), prev.Currency)
	desc := preferLonger(attrStr(mergedAttrs, "description"), prev.Description)
	lang := preferNonEmpty(attrStr(mergedAttrs, "language"), prev.Language)
	remoteType := preferNonEmpty(attrStr(mergedAttrs, "remote_type"), prev.RemoteType)
	applyURL := preferNonEmpty(attrStr(mergedAttrs, "apply_url"), prev.ApplyURL)
	salaryMin := preferNonZero(attrFloat(mergedAttrs, "amount_min"), prev.SalaryMin)
	salaryMax := preferNonZero(attrFloat(mergedAttrs, "amount_max"), prev.SalaryMax)

	// CanonicalID == ClusterID == in.OpportunityID. Dedup allocates
	// the cluster_id; canonical_id is the same xid (one logical
	// identity per opportunity). The kv_rebuild path reconstructs
	// this invariant from slug JSON, so they must agree at write
	// time too.
	merged := kv.ClusterSnapshot{
		ClusterID:    in.OpportunityID,
		CanonicalID:  in.OpportunityID,
		Slug:         prev.Slug,
		Kind:         in.Kind,
		Title:        title,
		Company:      issuer,
		Description:  desc,
		Country:      country,
		Language:     lang,
		RemoteType:   remoteType,
		SalaryMin:    salaryMin,
		SalaryMax:    salaryMax,
		Currency:     currency,
		Status:       "active",
		LastSeenAt:   now,
		PostedAt:     prev.PostedAt,
		ApplyURL:     applyURL,
		QualityScore: prev.QualityScore,
		Attributes:   mergedAttrs,
	}
	if prev.FirstSeenAt.IsZero() {
		merged.FirstSeenAt = now
	} else {
		merged.FirstSeenAt = prev.FirstSeenAt
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
		Attributes:    merged.Attributes,
		UpsertedAt:    now,
	}
	outEnv := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsUpserted, out)
	// Emit the canonical-upserted event for the (in-process) publish
	// handler — it's a fast R2 write, not external LLM I/O, so it
	// stays on the events bus.
	if err := h.svc.EventsManager().Emit(ctx, eventsv1.TopicCanonicalsUpserted, outEnv); err != nil {
		return err
	}
	// Also publish to the durable Queue subjects for embed + translate.
	// Both are external LLM calls (TEI + Groq) that need retry-with-
	// backoff per the Frame async decision tree. The body is the same
	// envelope; only the transport differs.
	body, err := json.Marshal(outEnv)
	if err != nil {
		return fmt.Errorf("canonical: marshal: %w", err)
	}
	qm := h.svc.QueueManager()
	if pubErr := qm.Publish(ctx, eventsv1.SubjectWorkerEmbed, body); pubErr != nil {
		// A queue publish failure mustn't block the rest of the
		// pipeline (publish handler already ran above). Log and continue
		// — the canonical row is still in cache + R2; a re-emission on
		// the next variant for the same opportunity will retry.
		util.Log(ctx).WithError(pubErr).
			WithField("subject", eventsv1.SubjectWorkerEmbed).
			Warn("canonical: queue publish failed, continuing")
	}
	if pubErr := qm.Publish(ctx, eventsv1.SubjectWorkerTranslate, body); pubErr != nil {
		util.Log(ctx).WithError(pubErr).
			WithField("subject", eventsv1.SubjectWorkerTranslate).
			Warn("canonical: queue publish failed, continuing")
	}
	return nil
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

func attrStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func attrFloat(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
