package service

// arrow_build.go — per-type Go-struct-to-Arrow-RecordBatch builders.
//
// Each BuildXxxRecord function:
//  1. Decodes a slice of raw envelope JSON into the typed payload.
//  2. Appends every field to the corresponding column builder.
//  3. Returns a RecordReader wrapping a single RecordBatch.
//
// Nullable field rule:
//   - String: append null if the Go value is "".
//   - float64: append null if value == 0 AND the parquet tag says ",optional".
//   - int / int32: append null if value == 0 AND optional.
//   - time.Time: append null if t.IsZero().
//   - []T: append null if slice is nil; otherwise append list contents.
//
// Callers MUST call Release() on the returned RecordReader when done.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// --------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------

// appendOptStr appends s to b; if s == "" the value is null.
func appendOptStr(b *array.StringBuilder, s string) {
	if s == "" {
		b.AppendNull()
	} else {
		b.Append(s)
	}
}

// appendOptF64 appends v unconditionally.
//
// Zero is a valid value for salary_min, salary_max, quality_score, and
// rerank_score (e.g. volunteer postings, unranked candidates). Treating zero
// as null was a data-inconsistency bug: Manticore uint64 encoding lands as 0
// regardless, but analytical Parquet queries would see nulls instead of zeros.
// Callers that genuinely mean "unknown / not set" should pass math.NaN() and
// handle that value explicitly.
func appendOptF64(b *array.Float64Builder, v float64) {
	b.Append(v)
}

// appendOptI32 appends v; null only when v == 0 and the field is semantically
// nullable (e.g. http_status where 0 means "request never made").
//
// Fields like years_experience, salary_min/max that carry the integer values
// of CV-extracted data remain here because zero is still a valid boundary
// value (career-changer with no paid experience). The original zero-as-null
// behaviour is preserved for these fields; callers that need strict non-null
// semantics should Append directly on the builder.
func appendOptI32(b *array.Int32Builder, v int) {
	if v == 0 {
		b.AppendNull()
	} else {
		b.Append(int32(v))
	}
}

// appendOptI64 appends v; null if v == 0 (optional int64).
func appendOptI64(b *array.Int64Builder, v int64) {
	if v == 0 {
		b.AppendNull()
	} else {
		b.Append(v)
	}
}

// appendTS appends a required timestamp (microseconds since epoch).
func appendTS(b *array.TimestampBuilder, t time.Time) {
	b.Append(arrow.Timestamp(t.UTC().UnixMicro()))
}

// appendOptTS appends a nullable timestamp; null if t.IsZero().
func appendOptTS(b *array.TimestampBuilder, t time.Time) {
	if t.IsZero() {
		b.AppendNull()
	} else {
		b.Append(arrow.Timestamp(t.UTC().UnixMicro()))
	}
}

// appendStrList appends a nullable list-of-strings.
// nil slice → null list; otherwise appends each element as a valid string.
func appendStrList(lb *array.ListBuilder, vals []string) {
	if vals == nil {
		lb.AppendNull()
		return
	}
	lb.Append(true)
	vb := lb.ValueBuilder().(*array.StringBuilder)
	for _, s := range vals {
		vb.Append(s)
	}
}

// appendF32List appends a non-nullable list-of-float32 (used for
// candidate-embedding vectors). An empty slice appends an empty list
// (not null).
func appendF32List(lb *array.ListBuilder, vals []float32) {
	lb.Append(true)
	vb := lb.ValueBuilder().(*array.Float32Builder)
	for _, v := range vals {
		vb.Append(v)
	}
}

// appendF32ListAsF64 widens a wire-format []float32 into a List<Double>
// — used for opportunities.embeddings, where the Iceberg schema is
// List<Double> for analytics-friendly width-agnostic encoding while
// the EmbeddingV1 wire payload still ships []float32.
func appendF32ListAsF64(lb *array.ListBuilder, vals []float32) {
	lb.Append(true)
	vb := lb.ValueBuilder().(*array.Float64Builder)
	for _, v := range vals {
		vb.Append(float64(v))
	}
}

// appendOptBool appends v unconditionally — bool has no natural "null"
// sentinel; callers that genuinely don't know append null directly.
// Phase 3.2 keeps Remote nullable in Iceberg but the Go wire type is a
// plain bool, so we forward false as a real value.
func appendOptBool(b *array.BooleanBuilder, v bool) {
	b.Append(v)
}

// appendOptDeadline appends a *time.Time deadline; null when nil or zero.
func appendOptDeadline(b *array.TimestampBuilder, t *time.Time) {
	if t == nil || t.IsZero() {
		b.AppendNull()
		return
	}
	b.Append(arrow.Timestamp(t.UTC().UnixMicro()))
}

// attrString returns the string value for key from attrs, or "" when
// the key is missing or carries a non-string value.
func attrString(attrs map[string]any, key string) string {
	if v, ok := attrs[key].(string); ok {
		return v
	}
	return ""
}

// attrFloat64 returns the float64 value for key from attrs, or 0 when
// missing. Zero is a valid float — caller checks "_, ok" semantics by
// using attrFloat64Opt when nullability matters.
func attrFloat64Opt(attrs map[string]any, key string) (float64, bool) {
	if v, ok := attrs[key].(float64); ok {
		return v, true
	}
	return 0, false
}

// attrTimestamp returns the time value for key from attrs by parsing
// an RFC3339 string, or zero time when missing/unparseable. Used for
// the optional deadline column on variants — absent in the legacy
// VariantIngestedV1 envelope, often present in Attributes.
func attrTimestamp(attrs map[string]any, key string) time.Time {
	s, ok := attrs[key].(string)
	if !ok || s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// makeRecordReader wraps a single RecordBatch into a RecordReader.
// The batch is released by the caller (via the reader's Release).
func makeRecordReader(schema *arrow.Schema, rec arrow.RecordBatch) (array.RecordReader, error) {
	rdr, err := array.NewRecordReader(schema, []arrow.RecordBatch{rec})
	if err != nil {
		return nil, err
	}
	return rdr, nil
}

// --------------------------------------------------------------------
// opportunities.variants  (polymorphic Opportunity — all variant
// pipeline stages share this Iceberg table, distinguished by the
// "stage" column)
// --------------------------------------------------------------------

// BuildVariantIngestedRecord builds an Arrow record for
// opportunities.variants from VariantIngestedV1 envelopes.
//
// All universal columns read directly from the new event fields. The
// kind-specific blob lands in `attributes` as JSON; `categories` is
// comma-joined when present in Attributes (post-Phase-3.1 connectors
// surface it there until VariantIngestedV1 grows a typed slot).
func BuildVariantIngestedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantIngestedV1: %w", err)
		}
		appendPolyVariant(b, env.Payload, env.Payload.Stage, env.Payload.ScrapedAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantNormalizedRecord builds for VariantNormalizedV1.
//
// VariantNormalizedV1 only carries {VariantID, HardKey, Kind,
// NormalizedAt, Attributes}. The remaining universal columns are
// recovered from Attributes where the normalizer surfaced them; the
// rest land null. scraped_at is required, so we substitute
// NormalizedAt — the writer's timestamp invariant on this table is
// "the latest stage-event timestamp", not the original scrape moment.
func BuildVariantNormalizedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantNormalizedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantNormalizedV1: %w", err)
		}
		p := env.Payload
		// Synthesise an Opportunity-shaped record from the slim
		// stage payload + Attributes.
		stage := eventsv1.VariantIngestedV1{
			VariantID:     p.VariantID,
			HardKey:       p.HardKey,
			Kind:          p.Kind,
			Stage:         "normalized",
			Title:         attrString(p.Attributes, "title"),
			IssuingEntity: attrString(p.Attributes, "issuing_entity"),
			AnchorCountry: attrString(p.Attributes, "country"),
			AnchorRegion:  attrString(p.Attributes, "region"),
			AnchorCity:    attrString(p.Attributes, "city"),
			Currency:      attrString(p.Attributes, "currency"),
			Attributes:    p.Attributes,
		}
		if v, ok := attrFloat64Opt(p.Attributes, "amount_min"); ok {
			stage.AmountMin = v
		}
		if v, ok := attrFloat64Opt(p.Attributes, "amount_max"); ok {
			stage.AmountMax = v
		}
		appendPolyVariant(b, stage, "normalized", p.NormalizedAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantValidatedRecord builds for VariantValidatedV1. The slim
// stage event only carries identity + validation result; universal
// columns land null.
func BuildVariantValidatedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantValidatedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantValidatedV1: %w", err)
		}
		p := env.Payload
		stage := eventsv1.VariantIngestedV1{
			VariantID: p.VariantID,
			HardKey:   p.HardKey,
			Kind:      p.Kind,
			Stage:     "validated",
		}
		appendPolyVariant(b, stage, "validated", p.ValidatedAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantFlaggedRecord builds for VariantFlaggedV1.
func BuildVariantFlaggedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantFlaggedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantFlaggedV1: %w", err)
		}
		p := env.Payload
		stage := eventsv1.VariantIngestedV1{
			VariantID: p.VariantID,
			HardKey:   p.HardKey,
			Kind:      p.Kind,
			Stage:     "flagged",
		}
		appendPolyVariant(b, stage, "flagged", p.FlaggedAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantClusteredRecord builds for VariantClusteredV1.
func BuildVariantClusteredRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantClusteredV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantClusteredV1: %w", err)
		}
		p := env.Payload
		stage := eventsv1.VariantIngestedV1{
			VariantID: p.VariantID,
			HardKey:   p.HardKey,
			Kind:      p.Kind,
			Stage:     "clustered",
		}
		appendPolyVariant(b, stage, "clustered", p.ClusteredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// appendPolyVariant writes one row to a polymorphic-Opportunity Arrow
// builder (variants or published — both share _polyVariantFields).
//
// Field offsets MUST stay in lock-step with _polyVariantFields in
// arrow_schemas.go and the VARIANTS schema in
// definitions/iceberg/_schemas.py.
func appendPolyVariant(
	b *array.RecordBuilder,
	p eventsv1.VariantIngestedV1,
	stage string,
	scrapedAt time.Time,
) {
	// 0–6: required identity + discriminators + title.
	b.Field(0).(*array.StringBuilder).Append(p.VariantID)
	b.Field(1).(*array.StringBuilder).Append(p.SourceID)
	b.Field(2).(*array.StringBuilder).Append(p.ExternalID)
	b.Field(3).(*array.StringBuilder).Append(p.HardKey)
	b.Field(4).(*array.StringBuilder).Append(p.Kind)
	b.Field(5).(*array.StringBuilder).Append(stage)
	b.Field(6).(*array.StringBuilder).Append(p.Title)

	// 7–10: optional descriptors.
	appendOptStr(b.Field(7).(*array.StringBuilder), p.IssuingEntity)
	appendOptStr(b.Field(8).(*array.StringBuilder), p.AnchorCountry)
	appendOptStr(b.Field(9).(*array.StringBuilder), p.AnchorRegion)
	appendOptStr(b.Field(10).(*array.StringBuilder), p.AnchorCity)

	// 11–12: lat/lon — pulled from Attributes since
	// VariantIngestedV1 doesn't yet carry typed coordinates.
	if v, ok := attrFloat64Opt(p.Attributes, "lat"); ok {
		b.Field(11).(*array.Float64Builder).Append(v)
	} else {
		b.Field(11).(*array.Float64Builder).AppendNull()
	}
	if v, ok := attrFloat64Opt(p.Attributes, "lon"); ok {
		b.Field(12).(*array.Float64Builder).Append(v)
	} else {
		b.Field(12).(*array.Float64Builder).AppendNull()
	}

	// 13: remote (bool — append false as a real value).
	appendOptBool(b.Field(13).(*array.BooleanBuilder), p.Remote)

	// 14: geo_scope — also Attributes-sourced today.
	appendOptStr(b.Field(14).(*array.StringBuilder), attrString(p.Attributes, "geo_scope"))

	// 15–17: monetary.
	appendOptStr(b.Field(15).(*array.StringBuilder), p.Currency)
	appendOptF64(b.Field(16).(*array.Float64Builder), p.AmountMin)
	appendOptF64(b.Field(17).(*array.Float64Builder), p.AmountMax)

	// 18: deadline — Attributes-sourced (RFC3339 string).
	deadline := attrTimestamp(p.Attributes, "deadline")
	appendOptTS(b.Field(18).(*array.TimestampBuilder), deadline)

	// 19: categories — comma-joined string from Attributes (the
	// Phase 3.1 envelope doesn't carry a typed Categories slot;
	// connectors stash it in attributes["categories"] until then).
	appendOptStr(b.Field(19).(*array.StringBuilder), categoriesFromAttrs(p.Attributes))

	// 20: attributes JSON blob.
	appendOptStr(b.Field(20).(*array.StringBuilder), marshalAttrs(p.Attributes))

	// 21: scraped_at — required.
	appendTS(b.Field(21).(*array.TimestampBuilder), scrapedAt)
}

// categoriesFromAttrs joins a categories list from Attributes into a
// comma-separated string. Accepts either []any or []string. Empty/nil
// → "".
func categoriesFromAttrs(attrs map[string]any) string {
	v, ok := attrs["categories"]
	if !ok || v == nil {
		return ""
	}
	switch xs := v.(type) {
	case []string:
		return joinCSV(xs)
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return joinCSV(out)
	case string:
		return xs
	}
	return ""
}

func joinCSV(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	out := xs[0]
	for _, s := range xs[1:] {
		out += "," + s
	}
	return out
}

// marshalAttrs JSON-encodes attrs to a string. Empty/nil → "".
// Marshal failure (unlikely with map[string]any) returns "" and the
// caller writes null — losing the blob is preferable to dropping the
// row, since attributes is a search/diagnostic column and Iceberg
// commits are append-only.
func marshalAttrs(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		return ""
	}
	return string(b)
}

// BuildCanonicalUpsertedRecord and BuildCanonicalExpiredRecord are removed.
// jobs.canonicals and jobs.canonicals_expired are no longer written to Iceberg.
// Canonical body is published as R2-slug-direct JSON; expired is a Frame event only.

// --------------------------------------------------------------------
// opportunities.embeddings  (EmbeddingV1) — variant-keyed semantic
// vectors. Wire format remains []float32; Iceberg stores List<Double>.
// --------------------------------------------------------------------

func BuildEmbeddingRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaEmbeddings)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.EmbeddingV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode EmbeddingV1: %w", err)
		}
		p := env.Payload

		// EmbeddingV1 still carries OpportunityID (legacy field name);
		// Phase 3.x repurposes it as the variant key. ModelVersion is
		// the closest existing carrier for the kind discriminator;
		// Phase 4.x will add a typed Kind slot. Until then we leave
		// kind="" (the Iceberg column is required, so we substitute
		// "unknown" to keep commits succeeding).
		b.Field(0).(*array.StringBuilder).Append(p.OpportunityID)
		kind := "unknown"
		b.Field(1).(*array.StringBuilder).Append(kind)
		appendF32ListAsF64(b.Field(2).(*array.ListBuilder), p.Vector)
		// embedded_at — EmbeddingV1 has no explicit field; use envelope OccurredAt.
		appendTS(b.Field(3).(*array.TimestampBuilder), env.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaEmbeddings, rec)
}

// BuildTranslationRecord is removed.
// jobs.translations is no longer written to Iceberg.
// Translated body lives at s3://opportunities-content/jobs/<slug>/<lang>.json (R2-direct).

// --------------------------------------------------------------------
// opportunities.published  (PublishedV1) — Verify-passing variants.
//
// Mirrors opportunities.variants column-for-column. PublishedV1's
// current wire shape ({OpportunityID, Slug, Kind, R2Version,
// PublishedAt}) is much narrower than the new published schema; the
// Phase 3.1 transitional payload doesn't yet carry the full polymorphic
// envelope, so we emit a minimal record (identity + kind + stage +
// title=slug as a placeholder) and leave the rest null. Phase 4.x will
// thicken PublishedV1 to mirror VariantIngestedV1 and we'll route
// straight through appendPolyVariant.
// --------------------------------------------------------------------

func BuildPublishedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaPublished)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.PublishedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode PublishedV1: %w", err)
		}
		p := env.Payload

		stage := eventsv1.VariantIngestedV1{
			VariantID:  p.OpportunityID,
			SourceID:   "",
			ExternalID: "",
			HardKey:    "",
			Kind:       p.Kind,
			Stage:      "published",
			Title:      p.Slug, // placeholder until PublishedV1 carries Title.
		}
		appendPolyVariant(b, stage, "published", p.PublishedAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaPublished, rec)
}

// --------------------------------------------------------------------
// opportunities.variants_rejected  (Verify-stage rejection sink)
//
// One row per VariantRejectedV1 event. Reasons is serialised as a JSON
// array string so downstream SQL can `JSON_EXTRACT` the first element
// without a schema change when new categorical reasons are added.
// --------------------------------------------------------------------

func BuildVariantRejectedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariantsRejected)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantRejectedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantRejectedV1: %w", err)
		}
		p := env.Payload

		reasonsJSON, err := json.Marshal(p.Reasons)
		if err != nil {
			return nil, fmt.Errorf("marshal reasons: %w", err)
		}

		b.Field(0).(*array.StringBuilder).Append(p.VariantID)
		b.Field(1).(*array.StringBuilder).Append(p.SourceID)
		b.Field(2).(*array.StringBuilder).Append(p.Kind)
		b.Field(3).(*array.StringBuilder).Append(p.Title)
		b.Field(4).(*array.StringBuilder).Append(string(reasonsJSON))
		appendTS(b.Field(5).(*array.TimestampBuilder), p.RejectedAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariantsRejected, rec)
}

// --------------------------------------------------------------------
// jobs.crawl_page_completed  (CrawlPageCompletedV1)
// --------------------------------------------------------------------

func BuildCrawlPageCompletedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaCrawlPageCompleted)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.CrawlPageCompletedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode CrawlPageCompletedV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.RequestID)
		b.Field(1).(*array.StringBuilder).Append(p.SourceID)
		appendOptStr(b.Field(2).(*array.StringBuilder), p.URL)
		appendOptI32(b.Field(3).(*array.Int32Builder), p.HTTPStatus)
		b.Field(4).(*array.Int32Builder).Append(int32(p.JobsFound))
		b.Field(5).(*array.Int32Builder).Append(int32(p.JobsEmitted))
		b.Field(6).(*array.Int32Builder).Append(int32(p.JobsRejected))
		appendOptStr(b.Field(7).(*array.StringBuilder), p.Cursor)
		appendOptStr(b.Field(8).(*array.StringBuilder), p.ErrorCode)
		appendOptStr(b.Field(9).(*array.StringBuilder), p.ErrorMessage)
		b.Field(10).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(11).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaCrawlPageCompleted, rec)
}

// --------------------------------------------------------------------
// jobs.sources_discovered  (SourceDiscoveredV1)
// --------------------------------------------------------------------

func BuildSourceDiscoveredRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaSourcesDiscovered)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.SourceDiscoveredV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode SourceDiscoveredV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.SourceID)
		b.Field(1).(*array.StringBuilder).Append(p.DiscoveredURL)
		appendOptStr(b.Field(2).(*array.StringBuilder), p.Name)
		appendOptStr(b.Field(3).(*array.StringBuilder), p.Country)
		appendOptStr(b.Field(4).(*array.StringBuilder), p.Type)
		b.Field(5).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(6).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaSourcesDiscovered, rec)
}

// --------------------------------------------------------------------
// candidates.cv_uploaded  (CVUploadedV1)
// --------------------------------------------------------------------

func BuildCVUploadedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaCVUploaded)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.CVUploadedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode CVUploadedV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CandidateID)
		b.Field(1).(*array.Int32Builder).Append(int32(p.CVVersion))
		b.Field(2).(*array.StringBuilder).Append(p.RawArchiveRef)
		appendOptStr(b.Field(3).(*array.StringBuilder), p.Filename)
		appendOptStr(b.Field(4).(*array.StringBuilder), p.ContentType)
		appendOptI64(b.Field(5).(*array.Int64Builder), p.SizeBytes)
		b.Field(6).(*array.StringBuilder).Append(p.ExtractedText)
		b.Field(7).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(8).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaCVUploaded, rec)
}

// --------------------------------------------------------------------
// candidates.cv_extracted  (CVExtractedV1)
// --------------------------------------------------------------------

func BuildCVExtractedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaCVExtracted)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.CVExtractedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode CVExtractedV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CandidateID)
		b.Field(1).(*array.Int32Builder).Append(int32(p.CVVersion))
		appendOptStr(b.Field(2).(*array.StringBuilder), p.Name)
		appendOptStr(b.Field(3).(*array.StringBuilder), p.Email)
		appendOptStr(b.Field(4).(*array.StringBuilder), p.Phone)
		appendOptStr(b.Field(5).(*array.StringBuilder), p.Location)
		appendOptStr(b.Field(6).(*array.StringBuilder), p.CurrentTitle)
		appendOptStr(b.Field(7).(*array.StringBuilder), p.Bio)
		appendOptStr(b.Field(8).(*array.StringBuilder), p.Seniority)
		appendOptI32(b.Field(9).(*array.Int32Builder), p.YearsExperience)
		appendOptStr(b.Field(10).(*array.StringBuilder), p.PrimaryIndustry)
		appendStrList(b.Field(11).(*array.ListBuilder), p.StrongSkills)
		appendStrList(b.Field(12).(*array.ListBuilder), p.WorkingSkills)
		appendStrList(b.Field(13).(*array.ListBuilder), p.ToolsFrameworks)
		appendStrList(b.Field(14).(*array.ListBuilder), p.Certifications)
		appendStrList(b.Field(15).(*array.ListBuilder), p.PreferredRoles)
		appendStrList(b.Field(16).(*array.ListBuilder), p.Languages)
		appendOptStr(b.Field(17).(*array.StringBuilder), p.Education)
		appendStrList(b.Field(18).(*array.ListBuilder), p.PreferredLocations)
		appendOptStr(b.Field(19).(*array.StringBuilder), p.RemotePreference)
		appendOptI32(b.Field(20).(*array.Int32Builder), p.SalaryMin)
		appendOptI32(b.Field(21).(*array.Int32Builder), p.SalaryMax)
		appendOptStr(b.Field(22).(*array.StringBuilder), p.Currency)
		b.Field(23).(*array.Int32Builder).Append(int32(p.ScoreATS))
		b.Field(24).(*array.Int32Builder).Append(int32(p.ScoreKeywords))
		b.Field(25).(*array.Int32Builder).Append(int32(p.ScoreImpact))
		b.Field(26).(*array.Int32Builder).Append(int32(p.ScoreRoleFit))
		b.Field(27).(*array.Int32Builder).Append(int32(p.ScoreClarity))
		b.Field(28).(*array.Int32Builder).Append(int32(p.ScoreOverall))
		appendOptStr(b.Field(29).(*array.StringBuilder), p.ModelVersionExtract)
		appendOptStr(b.Field(30).(*array.StringBuilder), p.ModelVersionScore)
		b.Field(31).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(32).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaCVExtracted, rec)
}

// --------------------------------------------------------------------
// candidates.cv_improved  (CVImprovedV1)
// Fixes is []CVFix — List<Struct<...>>
// --------------------------------------------------------------------

func BuildCVImprovedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaCVImproved)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.CVImprovedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode CVImprovedV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CandidateID)
		b.Field(1).(*array.Int32Builder).Append(int32(p.CVVersion))

		// fixes — List<Struct>
		lb := b.Field(2).(*array.ListBuilder)
		sb := lb.ValueBuilder().(*array.StructBuilder)
		lb.Append(true)
		for _, fix := range p.Fixes {
			sb.Append(true)
			sb.FieldBuilder(0).(*array.StringBuilder).Append(fix.FixID)
			appendOptStr(sb.FieldBuilder(1).(*array.StringBuilder), fix.Title)
			appendOptStr(sb.FieldBuilder(2).(*array.StringBuilder), fix.ImpactLevel)
			appendOptStr(sb.FieldBuilder(3).(*array.StringBuilder), fix.Category)
			appendOptStr(sb.FieldBuilder(4).(*array.StringBuilder), fix.Why)
			sb.FieldBuilder(5).(*array.BooleanBuilder).Append(fix.AutoApplicable)
			appendOptStr(sb.FieldBuilder(6).(*array.StringBuilder), fix.Rewrite)
		}

		appendOptStr(b.Field(3).(*array.StringBuilder), p.ModelVersion)
		b.Field(4).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(5).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaCVImproved, rec)
}

// --------------------------------------------------------------------
// candidates.embeddings  (CandidateEmbeddingV1)
// --------------------------------------------------------------------

func BuildCandidateEmbeddingRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaCandidateEmbeddings)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode CandidateEmbeddingV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CandidateID)
		b.Field(1).(*array.Int32Builder).Append(int32(p.CVVersion))
		appendF32List(b.Field(2).(*array.ListBuilder), p.Vector)
		appendOptStr(b.Field(3).(*array.StringBuilder), p.ModelVersion)
		b.Field(4).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(5).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaCandidateEmbeddings, rec)
}

// --------------------------------------------------------------------
// candidates.preferences  (PreferencesUpdatedV1)
// --------------------------------------------------------------------

func BuildPreferencesRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaPreferences)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode PreferencesUpdatedV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CandidateID)
		appendOptStr(b.Field(1).(*array.StringBuilder), p.RemotePreference)
		appendOptI32(b.Field(2).(*array.Int32Builder), p.SalaryMin)
		appendOptI32(b.Field(3).(*array.Int32Builder), p.SalaryMax)
		appendOptStr(b.Field(4).(*array.StringBuilder), p.Currency)
		appendStrList(b.Field(5).(*array.ListBuilder), p.PreferredLocations)
		appendStrList(b.Field(6).(*array.ListBuilder), p.ExcludedCompanies)
		appendStrList(b.Field(7).(*array.ListBuilder), p.TargetRoles)
		appendStrList(b.Field(8).(*array.ListBuilder), p.Languages)
		appendOptStr(b.Field(9).(*array.StringBuilder), p.Availability)
		b.Field(10).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(11).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaPreferences, rec)
}

// --------------------------------------------------------------------
// candidates.matches_ready  (MatchesReadyV1)
// Matches is []MatchRow — List<Struct<...>>
// --------------------------------------------------------------------

func BuildMatchesReadyRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaMatchesReady)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.MatchesReadyV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode MatchesReadyV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CandidateID)
		b.Field(1).(*array.StringBuilder).Append(p.MatchBatchID)

		// matches — List<Struct>
		lb := b.Field(2).(*array.ListBuilder)
		sb := lb.ValueBuilder().(*array.StructBuilder)
		lb.Append(true)
		for _, m := range p.Matches {
			sb.Append(true)
			sb.FieldBuilder(0).(*array.StringBuilder).Append(m.CanonicalID)
			sb.FieldBuilder(1).(*array.Float64Builder).Append(m.Score)
			appendOptF64(sb.FieldBuilder(2).(*array.Float64Builder), m.RerankScore)
		}

		b.Field(3).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(4).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaMatchesReady, rec)
}
