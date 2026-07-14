package eventsv1

// OpportunityEmbedV1 is the durable queue payload for embedding a
// stored opportunity. Published by the postgres ingest path after
// Complete succeeds; consumed by the worker's EmbedHandler (Frame
// Queue — external HTTP + retry/backoff).
//
// OpportunityID is opportunities.canonical_id. Title/IssuingEntity/
// Description are the same fields EmbedInput uses so live and backfill
// produce comparable vectors.
type OpportunityEmbedV1 struct {
	OpportunityID string `json:"opportunity_id"`
	Title         string `json:"title"`
	IssuingEntity string `json:"issuing_entity,omitempty"`
	Description   string `json:"description,omitempty"`
}
