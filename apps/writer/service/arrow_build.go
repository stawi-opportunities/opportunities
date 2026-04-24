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

	eventsv1 "stawi.jobs/pkg/events/v1"
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
// embedding vectors). An empty slice appends an empty list (not null).
func appendF32List(lb *array.ListBuilder, vals []float32) {
	lb.Append(true)
	vb := lb.ValueBuilder().(*array.Float32Builder)
	for _, v := range vals {
		vb.Append(v)
	}
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
// jobs.variants  (VariantIngestedV1 — all variant pipeline stages
// write to the same Iceberg table jobs.variants, distinguished by
// the "stage" field)
// --------------------------------------------------------------------

// BuildVariantIngestedRecord builds an Arrow record for the jobs.variants
// table from VariantIngestedV1 envelopes.
func BuildVariantIngestedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantIngestedV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		appendVariantFields(b, p.VariantID, p.SourceID, p.ExternalID, p.HardKey, p.Stage,
			p.Title, p.Company, p.LocationText, p.Country, p.Language,
			p.RemoteType, p.EmploymentType, p.SalaryMin, p.SalaryMax,
			p.Currency, p.Description, p.ApplyURL, p.PostedAt, p.ScrapedAt,
			p.ContentHash, p.RawArchiveRef, p.ModelVersionExtract,
			p.EventID, p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantNormalizedRecord builds for VariantNormalizedV1.
// Routes to jobs.variants with stage field filled from payload.
func BuildVariantNormalizedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantNormalizedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantNormalizedV1: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		appendVariantFields(b, p.VariantID, p.SourceID, p.ExternalID, p.HardKey, p.Stage,
			p.Title, p.Company, p.LocationText, p.Country, p.Language,
			p.RemoteType, p.EmploymentType, p.SalaryMin, p.SalaryMax,
			p.Currency, p.Description, p.ApplyURL, p.PostedAt, p.ScrapedAt,
			p.ContentHash, p.RawArchiveRef, "" /*model_version_extract not on normalized*/,
			p.EventID, p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantValidatedRecord builds for VariantValidatedV1.
// Routes to jobs.variants — the embedded Normalized payload supplies
// the variant columns; stage is overwritten as "validated".
func BuildVariantValidatedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantValidatedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantValidatedV1: %w", err)
		}
		v := env.Payload
		n := v.Normalized
		appendVariantFields(b, n.VariantID, n.SourceID, n.ExternalID, n.HardKey,
			"validated",
			n.Title, n.Company, n.LocationText, n.Country, n.Language,
			n.RemoteType, n.EmploymentType, n.SalaryMin, n.SalaryMax,
			n.Currency, n.Description, n.ApplyURL, n.PostedAt, n.ScrapedAt,
			n.ContentHash, n.RawArchiveRef, "" /*no model_version_extract*/,
			env.EventID, env.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantFlaggedRecord builds for VariantFlaggedV1.
// Routes to jobs.variants. Only variant_id, source_id, stage are
// meaningful; remaining variant columns are null.
func BuildVariantFlaggedRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantFlaggedV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantFlaggedV1: %w", err)
		}
		p := env.Payload
		appendVariantFields(b, p.VariantID, p.SourceID, "" /*external_id*/, "" /*hard_key*/,
			"flagged",
			"" /*title*/, "" /*company*/, "" /*location_text*/, "" /*country*/, "" /*language*/,
			"" /*remote_type*/, "" /*employment_type*/, 0 /*salary_min*/, 0 /*salary_max*/,
			"" /*currency*/, "" /*description*/, "" /*apply_url*/,
			time.Time{} /*posted_at*/, time.Time{} /*scraped_at*/,
			"" /*content_hash*/, "" /*raw_archive_ref*/, "" /*model_version_extract*/,
			env.EventID, env.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// BuildVariantClusteredRecord builds for VariantClusteredV1.
// Routes to jobs.variants via the embedded Validated.Normalized chain.
func BuildVariantClusteredRecord(pool memory.Allocator, raws []json.RawMessage) (array.RecordReader, error) {
	b := array.NewRecordBuilder(pool, ArrowSchemaVariants)
	defer b.Release()

	for _, raw := range raws {
		var env eventsv1.Envelope[eventsv1.VariantClusteredV1]
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode VariantClusteredV1: %w", err)
		}
		c := env.Payload
		n := c.Validated.Normalized
		appendVariantFields(b, n.VariantID, n.SourceID, n.ExternalID, n.HardKey,
			"clustered",
			n.Title, n.Company, n.LocationText, n.Country, n.Language,
			n.RemoteType, n.EmploymentType, n.SalaryMin, n.SalaryMax,
			n.Currency, n.Description, n.ApplyURL, n.PostedAt, n.ScrapedAt,
			n.ContentHash, n.RawArchiveRef, "" /*no model_version_extract*/,
			env.EventID, env.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaVariants, rec)
}

// appendVariantFields is the shared column-append logic for all
// variant-family builders (same physical Arrow schema = jobs.variants).
func appendVariantFields(
	b *array.RecordBuilder,
	variantID, sourceID, externalID, hardKey, stage string,
	title, company, locationText, country, language,
	remoteType, employmentType string,
	salaryMin, salaryMax float64,
	currency, description, applyURL string,
	postedAt, scrapedAt time.Time,
	contentHash, rawArchiveRef, modelVersionExtract string,
	eventID string,
	occurredAt time.Time,
) {
	b.Field(0).(*array.StringBuilder).Append(variantID)
	b.Field(1).(*array.StringBuilder).Append(sourceID)
	b.Field(2).(*array.StringBuilder).Append(externalID)
	b.Field(3).(*array.StringBuilder).Append(hardKey)
	b.Field(4).(*array.StringBuilder).Append(stage)
	appendOptStr(b.Field(5).(*array.StringBuilder), title)
	appendOptStr(b.Field(6).(*array.StringBuilder), company)
	appendOptStr(b.Field(7).(*array.StringBuilder), locationText)
	appendOptStr(b.Field(8).(*array.StringBuilder), country)
	appendOptStr(b.Field(9).(*array.StringBuilder), language)
	appendOptStr(b.Field(10).(*array.StringBuilder), remoteType)
	appendOptStr(b.Field(11).(*array.StringBuilder), employmentType)
	appendOptF64(b.Field(12).(*array.Float64Builder), salaryMin)
	appendOptF64(b.Field(13).(*array.Float64Builder), salaryMax)
	appendOptStr(b.Field(14).(*array.StringBuilder), currency)
	appendOptStr(b.Field(15).(*array.StringBuilder), description)
	appendOptStr(b.Field(16).(*array.StringBuilder), applyURL)
	appendOptTS(b.Field(17).(*array.TimestampBuilder), postedAt)
	appendOptTS(b.Field(18).(*array.TimestampBuilder), scrapedAt)
	appendOptStr(b.Field(19).(*array.StringBuilder), contentHash)
	appendOptStr(b.Field(20).(*array.StringBuilder), rawArchiveRef)
	appendOptStr(b.Field(21).(*array.StringBuilder), modelVersionExtract)
	b.Field(22).(*array.StringBuilder).Append(eventID)
	appendTS(b.Field(23).(*array.TimestampBuilder), occurredAt)
}

// BuildCanonicalUpsertedRecord and BuildCanonicalExpiredRecord are removed.
// jobs.canonicals and jobs.canonicals_expired are no longer written to Iceberg.
// Canonical body is published as R2-slug-direct JSON; expired is a Frame event only.

// --------------------------------------------------------------------
// jobs.embeddings  (EmbeddingV1)
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
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CanonicalID)
		appendF32List(b.Field(1).(*array.ListBuilder), p.Vector)
		b.Field(2).(*array.StringBuilder).Append(p.ModelVersion)
		b.Field(3).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(4).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaEmbeddings, rec)
}

// BuildTranslationRecord is removed.
// jobs.translations is no longer written to Iceberg.
// Translated body lives at s3://stawi-jobs-content/jobs/<slug>/<lang>.json (R2-direct).

// --------------------------------------------------------------------
// jobs.published  (PublishedV1)
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
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt

		b.Field(0).(*array.StringBuilder).Append(p.CanonicalID)
		b.Field(1).(*array.StringBuilder).Append(p.Slug)
		b.Field(2).(*array.Int32Builder).Append(int32(p.R2Version))
		appendTS(b.Field(3).(*array.TimestampBuilder), p.PublishedAt)
		b.Field(4).(*array.StringBuilder).Append(p.EventID)
		appendTS(b.Field(5).(*array.TimestampBuilder), p.OccurredAt)
	}

	rec := b.NewRecord()
	defer rec.Release()
	return makeRecordReader(ArrowSchemaPublished, rec)
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
