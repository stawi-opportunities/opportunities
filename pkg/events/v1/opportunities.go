// Package eventsv1 carries the wire-format event types. The reshape from
// job-only to polymorphic Opportunity moves kind-specific fields into
// Attributes and adds a Kind discriminator to every payload.
package eventsv1

import "time"

// VariantIngestedV1 is the first event a record emits after a connector
// has produced it. It carries everything needed for partition pruning
// without unpacking Attributes.
type VariantIngestedV1 struct {
	VariantID  string `json:"variant_id"`
	SourceID   string `json:"source_id"`
	ExternalID string `json:"external_id"`
	HardKey    string `json:"hard_key"`

	Kind  string `json:"kind"`
	Stage string `json:"stage"` // "ingested" | "extracted" | "normalized" | "validated"

	// Universal envelope fields (denormalised for analytics queries that
	// don't want to JSON-decode Attributes).
	Title         string `json:"title"`
	IssuingEntity string `json:"issuing_entity,omitempty"`
	AnchorCountry string `json:"anchor_country,omitempty"` // ISO 3166-1 alpha-2
	AnchorRegion  string `json:"anchor_region,omitempty"`
	AnchorCity    string `json:"anchor_city,omitempty"`
	Remote        bool   `json:"remote,omitempty"`

	Currency  string  `json:"currency,omitempty"`
	AmountMin float64 `json:"amount_min,omitempty"`
	AmountMax float64 `json:"amount_max,omitempty"`

	// Polymorphic — JSON-encoded for transport, decoded by consumers
	// that need kind-specific fields.
	Attributes map[string]any `json:"attributes,omitempty"`

	ScrapedAt time.Time `json:"scraped_at"`
}
