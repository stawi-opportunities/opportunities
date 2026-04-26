package eventsv1

import "time"

// VariantNormalizedV1 — post-normalize stage. Attributes carries
// the kind-specific normalized fields.
type VariantNormalizedV1 struct {
	VariantID    string         `json:"variant_id"`
	HardKey      string         `json:"hard_key"`
	Kind         string         `json:"kind"`
	NormalizedAt time.Time      `json:"normalized_at"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// VariantValidatedV1 — emitted when a variant passes the AI
// validator with confidence >= threshold. Carries validation metadata.
type VariantValidatedV1 struct {
	VariantID    string    `json:"variant_id"`
	HardKey      string    `json:"hard_key"`
	Kind         string    `json:"kind"`
	Valid        bool      `json:"valid"`
	Reasons      []string  `json:"reasons,omitempty"`
	ValidatedAt  time.Time `json:"validated_at"`
	QualityScore float64   `json:"quality_score,omitempty"`
}

// VariantFlaggedV1 — emitted when a variant fails validation. Terminal
// for the happy path; audit sink.
type VariantFlaggedV1 struct {
	VariantID    string    `json:"variant_id"`
	HardKey      string    `json:"hard_key"`
	Kind         string    `json:"kind"`
	Reason       string    `json:"reason"`
	Confidence   float64   `json:"confidence,omitempty"`
	ModelVersion string    `json:"model_version,omitempty"`
	FlaggedAt    time.Time `json:"flagged_at"`
}

// VariantClusteredV1 — emitted post-dedup. Identifies which cluster
// (opportunity) this variant belongs to.
type VariantClusteredV1 struct {
	VariantID     string    `json:"variant_id"`
	OpportunityID string    `json:"opportunity_id"`
	HardKey       string    `json:"hard_key"`
	Kind          string    `json:"kind"`
	IsNew         bool      `json:"is_new"`
	ClusteredAt   time.Time `json:"clustered_at"`
}

// TranslationV1 — emitted by the translate handler once an opportunity
// has been translated to a single target language. One event per
// (opportunity, lang) pair.
type TranslationV1 struct {
	OpportunityID string    `json:"opportunity_id"`
	Lang          string    `json:"lang"`
	TitleTr       string    `json:"title_tr,omitempty"`
	DescriptionTr string    `json:"description_tr,omitempty"`
	ModelVersion  string    `json:"model_version,omitempty"`
	TranslatedAt  time.Time `json:"translated_at"`
}

// PublishedV1 — emitted by the publish handler after an opportunity's
// R2 snapshot is written. Downstream analytics + cache-purge listeners
// consume this.
type PublishedV1 struct {
	OpportunityID string    `json:"opportunity_id"`
	Slug          string    `json:"slug"`
	Kind          string    `json:"kind"`
	R2Version     int       `json:"r2_version"`
	PublishedAt   time.Time `json:"published_at"`
}
