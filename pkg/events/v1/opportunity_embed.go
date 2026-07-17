package eventsv1

// OpportunityEmbedV1 is the durable queue payload for embedding a
// stored opportunity. Published by the postgres ingest path after
// Complete succeeds; consumed by the worker's EmbedHandler (Frame
// Queue — external HTTP + retry/backoff).
//
// OpportunityID is opportunities.canonical_id. Title/IssuingEntity/
// Description are the same fields EmbedInput uses so live and backfill
// produce comparable vectors. Kind/Country/AmountMax/PostedAt ride
// along so Path A FanOut can score without a second DB round-trip.
type OpportunityEmbedV1 struct {
	OpportunityID string   `json:"opportunity_id"`
	Title         string   `json:"title"`
	IssuingEntity string   `json:"issuing_entity,omitempty"`
	Description   string   `json:"description,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Country       string   `json:"country,omitempty"`
	AmountMax     *float64 `json:"amount_max,omitempty"`
	// PostedAt is RFC3339 when known.
	PostedAt string `json:"posted_at,omitempty"`
}

// OpportunityFanOutV1 is published after a successful embed so the
// matching service can run Path A (opportunity → candidates) and collect
// matches without waiting for digest/gap-fill.
type OpportunityFanOutV1 struct {
	OpportunityID string    `json:"opportunity_id"`
	Title         string    `json:"title,omitempty"`
	IssuingEntity string    `json:"issuing_entity,omitempty"`
	Description   string    `json:"description,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	Country       string    `json:"country,omitempty"`
	AmountMax     *float64  `json:"amount_max,omitempty"`
	PostedAt      string    `json:"posted_at,omitempty"`
	Embedding     []float32 `json:"embedding"`
}
