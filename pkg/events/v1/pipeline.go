package eventsv1

import "time"

// VariantNormalizedV1 — post-normalize stage. Same fields as
// VariantIngestedV1 plus normalized versions of country (ISO 2),
// remote_type, and parsed salary numbers. Phase 3's normalize
// handler consumes VariantIngestedV1 and emits this.
type VariantNormalizedV1 struct {
	VariantID      string    `json:"variant_id"       parquet:"variant_id"`
	SourceID       string    `json:"source_id"        parquet:"source_id"`
	ExternalID     string    `json:"external_id"      parquet:"external_id"`
	HardKey        string    `json:"hard_key"         parquet:"hard_key"`
	Stage          string    `json:"stage"            parquet:"stage"`
	Title          string    `json:"title"            parquet:"title,optional"`
	Company        string    `json:"company"          parquet:"company,optional"`
	LocationText   string    `json:"location_text"    parquet:"location_text,optional"`
	Country        string    `json:"country"          parquet:"country,optional"`
	Language       string    `json:"language"         parquet:"language,optional"`
	RemoteType     string    `json:"remote_type"      parquet:"remote_type,optional"`
	EmploymentType string    `json:"employment_type"  parquet:"employment_type,optional"`
	SalaryMin      float64   `json:"salary_min"       parquet:"salary_min,optional"`
	SalaryMax      float64   `json:"salary_max"       parquet:"salary_max,optional"`
	Currency       string    `json:"currency"         parquet:"currency,optional"`
	Description    string    `json:"description"      parquet:"description,optional"`
	ApplyURL       string    `json:"apply_url"        parquet:"apply_url,optional"`
	PostedAt       time.Time `json:"posted_at"        parquet:"posted_at,optional"`
	ScrapedAt      time.Time `json:"scraped_at"       parquet:"scraped_at"`
	ContentHash    string    `json:"content_hash"     parquet:"content_hash,optional"`
	RawArchiveRef  string    `json:"raw_archive_ref"  parquet:"raw_archive_ref,optional"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}

// VariantValidatedV1 — emitted when a variant passes the AI
// validator with confidence >= threshold. Carries the variant
// state forward plus validation metadata.
type VariantValidatedV1 struct {
	VariantID       string `json:"variant_id"        parquet:"variant_id"`
	SourceID        string `json:"source_id"         parquet:"source_id"`
	ValidationScore float64 `json:"validation_score"  parquet:"validation_score"`
	ValidationNotes string `json:"validation_notes"  parquet:"validation_notes,optional"`
	ModelVersion    string `json:"model_version"     parquet:"model_version,optional"`
	// Normalized is the full previous-stage payload, so downstream
	// consumers (dedup, canonical) don't need to re-fetch.
	Normalized VariantNormalizedV1 `json:"normalized"       parquet:"normalized"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}

// VariantFlaggedV1 — emitted when a variant fails validation. Terminal
// for the happy path; audit sink.
type VariantFlaggedV1 struct {
	VariantID    string  `json:"variant_id"    parquet:"variant_id"`
	SourceID     string  `json:"source_id"     parquet:"source_id"`
	Reason       string  `json:"reason"        parquet:"reason"`
	Confidence   float64 `json:"confidence"    parquet:"confidence,optional"`
	ModelVersion string  `json:"model_version" parquet:"model_version,optional"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}

// VariantClusteredV1 — emitted post-dedup. Identifies which cluster
// this variant belongs to. Downstream canonical-merge uses cluster_id
// to look up the current snapshot + merge in the new variant's fields.
type VariantClusteredV1 struct {
	VariantID string             `json:"variant_id" parquet:"variant_id"`
	ClusterID string             `json:"cluster_id" parquet:"cluster_id"`
	IsNew     bool               `json:"is_new"     parquet:"is_new"`
	Validated VariantValidatedV1 `json:"validated"  parquet:"validated"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}

// TranslationV1 — emitted by the translate handler once a canonical
// has been translated to a single target language. One event per
// (canonical, lang) pair.
type TranslationV1 struct {
	CanonicalID   string `json:"canonical_id"   parquet:"canonical_id"`
	Lang          string `json:"lang"           parquet:"lang"`
	TitleTr       string `json:"title_tr"       parquet:"title_tr,optional"`
	DescriptionTr string `json:"description_tr" parquet:"description_tr,optional"`
	ModelVersion  string `json:"model_version"  parquet:"model_version,optional"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}

// PublishedV1 — emitted by the publish handler after a canonical's
// R2 snapshot is written. Downstream analytics + cache-purge listeners
// consume this.
type PublishedV1 struct {
	CanonicalID string    `json:"canonical_id" parquet:"canonical_id"`
	Slug        string    `json:"slug"         parquet:"slug"`
	R2Version   int       `json:"r2_version"   parquet:"r2_version"`
	PublishedAt time.Time `json:"published_at" parquet:"published_at"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}
