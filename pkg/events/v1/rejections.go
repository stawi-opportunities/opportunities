package eventsv1

import "time"

// VariantRejectedV1 is persisted in the append-only TimescaleDB ingestion
// event ledger when a crawler rejects a source record.
type VariantRejectedV1 struct {
	VariantID    string    `json:"variant_id"`
	SourceID     string    `json:"source_id"`
	Kind         string    `json:"kind"`
	Title        string    `json:"title"`
	Reasons      []string  `json:"reasons"`
	CrawlJobID   string    `json:"crawl_job_id,omitempty"`
	RejectedAt   time.Time `json:"rejected_at"`
}
