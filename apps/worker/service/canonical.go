package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/frame/queue"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
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
	store *variantstate.Store // nil-safe; soft-fails on Postgres outage
	next  string              // Queue Name for CanonicalUpsertedV1 (consumed by publish + sinks)
}

// NewCanonicalHandler binds the handler. next is the canonical Queue Name
// the publish stage (and the materializer/matching/writer sinks) consume.
func NewCanonicalHandler(svc *frame.Service, c cache.Cache[string, kv.ClusterSnapshot], store *variantstate.Store, next string) *CanonicalHandler {
	return &CanonicalHandler{svc: svc, cache: c, store: store, next: next}
}

var _ queue.SubscribeWorker = (*CanonicalHandler)(nil)

// Handle merges a VariantClusteredV1 into the cluster snapshot and
// publishes CanonicalUpsertedV1, then fans out to embed/translate.
func (h *CanonicalHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	var env eventsv1.Envelope[eventsv1.VariantClusteredV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return err
	}
	in := env.Payload

	prev, _, err := h.cache.Get(ctx, in.OpportunityID)
	if err != nil {
		util.Log(ctx).WithError(err).
			WithField("cluster_id", in.OpportunityID).
			Warn("canonical: cache.Get failed; starting from empty snapshot")
		prev = kv.ClusterSnapshot{}
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
		util.Log(ctx).WithError(err).
			WithField("cluster_id", merged.ClusterID).
			Warn("canonical: cache.Set failed; merge state not persisted")
	}

	out := eventsv1.CanonicalUpsertedV1{
		OpportunityID: merged.CanonicalID,
		Slug:          merged.Slug,
		HardKey:       in.HardKey,
		SourceID:      merged.SourceID,
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
	// Postgres opportunities UPSERT (Phase 4). Writes the same merged
	// snapshot to the canonical table — gives the API a Postgres-backed
	// view alongside the Valkey cache. JSONB attribute merge happens
	// inside the SQL; this struct just carries the latest fields.
	// Soft-fails — Postgres outage doesn't block the chain.
	oppRow := snapshotToOpportunity(merged, in.HardKey)
	_ = h.store.UpsertOpportunity(ctx, oppRow)

	// Company record: upsert the issuing organisation (name + logo + site),
	// linked to its jobs by issuing_entity. job_count grows only for newly seen
	// opportunities. Soft-fails inside the store — never blocks the chain.
	if slug := domain.CompanySlug(merged.Company); slug != "" {
		ptr := func(s string) *string {
			if s != "" {
				return &s
			}
			return nil
		}
		_ = h.store.UpsertCompany(ctx, slug, merged.Company,
			ptr(attrStr(merged.Attributes, "company_logo")),
			ptr(attrStr(merged.Attributes, "company_website")),
			ptr(merged.Country),
			prev.FirstSeenAt.IsZero())
	}

	outEnv := eventsv1.NewEnvelope(eventsv1.TopicCanonicalsUpserted, out)
	body, err := json.Marshal(outEnv)
	if err != nil {
		_ = h.store.RecordError(ctx, in.VariantID, variantstate.StageCanonical, fmt.Errorf("canonical: marshal: %w", err))
		return err
	}
	// Publish CanonicalUpsertedV1 to its queue. The publish stage (and the
	// materializer/matching/writer sinks) are durable consumers of it.
	if err := h.svc.QueueManager().Publish(ctx, h.next, body, nil); err != nil {
		_ = h.store.RecordError(ctx, in.VariantID, variantstate.StageClustered, fmt.Errorf("canonical: publish upserted: %w", err))
		return err
	}
	slug := merged.Slug
	_ = h.store.AdvanceStage(ctx, in.VariantID,
		variantstate.StageClustered, variantstate.StageCanonical,
		&merged.CanonicalID, &slug)
	// Fan out to the durable embed + translate queues (external LLM calls
	// that need their own retry/backoff per the Frame async decision tree).
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

// snapshotToOpportunity converts the in-memory merged ClusterSnapshot
// into the variantstate.Opportunity row shape for the UPSERT.
//
// hardKey is passed through into the Attributes map under the
// "hard_key" key so downstream consumers (admin sweeps, debug
// tooling) can correlate the canonical to its dedup key without
// re-querying pipeline_variants. The JSONB merge in
// UpsertOpportunity preserves it across UPSERTs.
//
// All pointer fields use the snapshot value verbatim — empty strings
// flow through as non-nil pointers, then COALESCE/NULLIF inside the
// SQL handles the "blank an established field" guard.
func snapshotToOpportunity(s kv.ClusterSnapshot, hardKey string) variantstate.Opportunity {
	attrs := s.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	if hardKey != "" {
		// non-mutating copy so we don't leak hard_key into the in-memory
		// snapshot kept by the cluster cache
		copyAttrs := make(map[string]any, len(attrs)+1)
		for k, v := range attrs {
			copyAttrs[k] = v
		}
		copyAttrs["hard_key"] = hardKey
		attrs = copyAttrs
	}
	remote := s.RemoteType == "remote"
	pPostedAt := (*time.Time)(nil)
	if !s.PostedAt.IsZero() {
		p := s.PostedAt
		pPostedAt = &p
	}
	pFirst := s.FirstSeenAt
	if pFirst.IsZero() {
		pFirst = time.Now().UTC()
	}
	pLast := s.LastSeenAt
	if pLast.IsZero() {
		pLast = time.Now().UTC()
	}
	srcID := s.SourceID
	desc := s.Description
	iss := s.Company
	country := s.Country
	apply := s.ApplyURL
	currency := s.Currency
	amin := s.SalaryMin
	amax := s.SalaryMax
	qs := s.QualityScore
	status := s.Status
	if status == "" {
		status = "active"
	}
	// Hot-field promotion: lift three filter-hot attribute keys to
	// top-level pointer fields. The original attrs map keeps the values
	// so the API still echoes them on read.
	extractAttrString := func(key string) *string {
		if v, ok := attrs[key].(string); ok && v != "" {
			return &v
		}
		return nil
	}
	employmentType := extractAttrString("employment_type")
	seniority := extractAttrString("seniority")
	geoScope := extractAttrString("geo_scope")
	return variantstate.Opportunity{
		CanonicalID:    s.CanonicalID,
		Slug:           s.Slug,
		Kind:           s.Kind,
		SourceID:       ptrIfNonEmpty(srcID),
		Title:          s.Title,
		Description:    ptrIfNonEmpty(desc),
		IssuingEntity:  ptrIfNonEmpty(iss),
		Country:        ptrIfNonEmpty(country),
		Remote:         &remote,
		ApplyURL:       ptrIfNonEmpty(apply),
		PostedAt:       pPostedAt,
		Currency:       ptrIfNonEmpty(currency),
		AmountMin:      ptrIfNonZero(amin),
		AmountMax:      ptrIfNonZero(amax),
		EmploymentType: employmentType,
		Seniority:      seniority,
		GeoScope:       geoScope,
		Status:         status,
		FirstSeenAt:    pFirst,
		LastSeenAt:     pLast,
		Attributes:     attrs,
		QualityScore:   ptrIfNonZero(qs),
	}
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrIfNonZero(f float64) *float64 {
	if f == 0 {
		return nil
	}
	return &f
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
