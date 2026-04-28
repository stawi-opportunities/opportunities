package eventsv1

import "time"

// CanonicalUpsertedV1 is the event emitted by the canonical-merge
// stage once a cluster of variants has been merged into a single
// user-facing opportunity row. The materializer consumes this event
// to populate idx_opportunities_rt — the Manticore index that backs
// search, browse, and detail.
type CanonicalUpsertedV1 struct {
	OpportunityID string `json:"opportunity_id"` // was canonical_job_id
	Slug          string `json:"slug"`
	HardKey       string `json:"hard_key"`

	Kind          string `json:"kind"`
	Title         string `json:"title"`
	IssuingEntity string `json:"issuing_entity"`
	ApplyURL      string `json:"apply_url"`

	AnchorCountry string  `json:"anchor_country,omitempty"`
	AnchorRegion  string  `json:"anchor_region,omitempty"`
	AnchorCity    string  `json:"anchor_city,omitempty"`
	Lat           float64 `json:"lat,omitempty"`
	Lon           float64 `json:"lon,omitempty"`
	Remote        bool    `json:"remote,omitempty"`
	GeoScope      string  `json:"geo_scope,omitempty"`

	PostedAt time.Time  `json:"posted_at"`
	Deadline *time.Time `json:"deadline,omitempty"`

	Currency  string  `json:"currency,omitempty"`
	AmountMin float64 `json:"amount_min,omitempty"`
	AmountMax float64 `json:"amount_max,omitempty"`

	Categories []string       `json:"categories,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`

	UpsertedAt time.Time `json:"upserted_at"`
}

// CanonicalExpiredV1 is emitted by the retention sweep when an
// opportunity's apply link is determined dead or its expires_at has
// passed. The materializer flips status to 'expired' on
// idx_opportunities_rt.
type CanonicalExpiredV1 struct {
	OpportunityID string    `json:"opportunity_id"`
	Reason        string    `json:"reason,omitempty"`
	ExpiredAt     time.Time `json:"expired_at"`
}

// EmbeddingV1 is the event emitted by the embedder stage once a
// canonical opportunity's semantic vector has been computed.
// Materializer updates the `embedding` HNSW attribute on
// idx_opportunities_rt; hybrid BM25+KNN queries consume it.
type EmbeddingV1 struct {
	OpportunityID string    `json:"opportunity_id"`
	Vector        []float32 `json:"vector"`
	ModelVersion  string    `json:"model_version"`
}

// OpportunityAutoFlaggedV1 is emitted when ≥ FlagAutoActionThreshold
// distinct unresolved scam flags from distinct users accumulate on
// the same canonical opportunity. The materializer subscribes and
// drops the row from search by pushing deadline to now (the
// polymorphic schema's single source of truth for liveness — search
// filters by `deadline > now`). Operator review still happens —
// auto-action is containment, not a final verdict.
//
// OpportunityID is the canonical's xid; the materializer maps it to
// Manticore's bigint pk via the same hashID() the rest of the
// pipeline uses. Slug is carried for log/audit context.
type OpportunityAutoFlaggedV1 struct {
	OpportunityID string    `json:"opportunity_id"`
	Slug          string    `json:"slug"`
	Kind          string    `json:"kind"`
	FlagCount     int       `json:"flag_count"`
	FlaggedAt     time.Time `json:"flagged_at"`
}
