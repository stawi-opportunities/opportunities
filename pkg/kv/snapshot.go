// Package kv exposes the typed values stored in Frame's cache
// framework by the pipeline workers. Transport and marshalling
// are handled by `frame/cache` + its backend (Valkey in prod,
// in-memory in tests). This package intentionally holds NO client
// code — just the shape of the cached values.
package kv

import "time"

// ClusterSnapshot is the compact canonical view held in the
// `cluster:{cluster_id}` cache so the canonical-merge handler can
// merge new variant fields without re-reading the full canonicals
// partition. Frame's GenericCache serializes this struct via its
// internal marshaller.
//
// Attributes carries the polymorphic per-kind fields (field_of_study,
// degree_level, procurement_domain, etc.) so the merge stage can
// emit them on CanonicalUpsertedV1 for the materializer's
// sparseColsForKind to read.
type ClusterSnapshot struct {
	ClusterID    string    `json:"cluster_id"`
	CanonicalID  string    `json:"canonical_id,omitempty"`
	Slug         string    `json:"slug,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Title        string    `json:"title,omitempty"`
	Company      string    `json:"company,omitempty"`
	Description  string    `json:"description,omitempty"`
	Country      string    `json:"country,omitempty"`
	Language     string    `json:"language,omitempty"`
	RemoteType   string    `json:"remote_type,omitempty"`
	SalaryMin    float64   `json:"salary_min,omitempty"`
	SalaryMax    float64   `json:"salary_max,omitempty"`
	Currency     string    `json:"currency,omitempty"`
	Category     string    `json:"category,omitempty"`
	QualityScore float64   `json:"quality_score,omitempty"`
	Status       string    `json:"status,omitempty"`
	FirstSeenAt  time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt   time.Time `json:"last_seen_at,omitempty"`
	PostedAt     time.Time `json:"posted_at,omitempty"`
	ApplyURL     string    `json:"apply_url,omitempty"`

	// Attributes is the polymorphic per-kind merge state. The dedup
	// stage seeds this from VariantValidatedV1.Attributes; the
	// canonical-merge stage reads it back and re-emits on
	// CanonicalUpsertedV1.
	Attributes map[string]any `json:"attributes,omitempty"`
}
