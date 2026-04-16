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
)

type VariantPayload struct {
	VariantID int64 `json:"variant_id"`
	SourceID  int64 `json:"source_id"`
}

type SourceURLsPayload struct {
	SourceID int64    `json:"source_id"`
	URLs     []string `json:"urls"`
}

type SourceQualityPayload struct {
	SourceID int64 `json:"source_id"`
}

type JobReadyPayload struct {
	CanonicalJobID int64 `json:"canonical_job_id"`
}
