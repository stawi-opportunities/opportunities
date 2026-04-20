package handlers

const (
	EventVariantRawStored     = "variant.raw.stored"
	EventVariantDeduped       = "variant.deduped"
	EventVariantNormalized    = "variant.normalized"
	EventVariantValidated     = "variant.validated"
	EventSourceURLsDiscovered = "source.urls.discovered"
	EventSourceQualityReview  = "source.quality.review"
	EventJobReady             = "job.ready"
	EventJobPublished         = "job.published"
	// EventCrawlRequest is emitted by the api's /admin/crawl/dispatch
	// endpoint (itself driven by a Trustage workflow on a cron) and
	// consumed by the crawler. Replaces the crawler's old in-process
	// ListDue loop — each message goes to exactly one worker, so
	// replicas can scale horizontally without contention.
	EventCrawlRequest = "crawl.request"
)

type VariantPayload struct {
	VariantID string `json:"variant_id"`
	SourceID  string `json:"source_id"`
}

type SourceURLsPayload struct {
	SourceID string   `json:"source_id"`
	URLs     []string `json:"urls"`
}

type SourceQualityPayload struct {
	SourceID string `json:"source_id"`
}

type JobReadyPayload struct {
	CanonicalJobID string `json:"canonical_job_id"`
}

type JobPublishedPayload struct {
	CanonicalJobID string `json:"canonical_job_id"`
	Slug           string `json:"slug"`
	SourceLang     string `json:"source_lang"`
	R2Version      int    `json:"r2_version"`
}

// CrawlRequestPayload carries a single "please crawl source N" instruction
// from Trustage (via the api's admin/crawl/dispatch endpoint) to one of
// the crawler's replicas. JetStream workqueue retention guarantees
// exactly-once delivery across the pod fleet.
type CrawlRequestPayload struct {
	SourceID string `json:"source_id"`
	// Attempt is 1 on first dispatch; Trustage's retry policy bumps it
	// when a prior run reported failure. Crawler logs it but doesn't
	// act on it — retry decisions live in Trustage.
	Attempt int `json:"attempt,omitempty"`
}
