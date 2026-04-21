package eventsv1

import "time"

// VariantIngestedV1 is the event emitted by the crawler once a raw
// job page has been fetched, archived, and AI-extracted. It carries
// the full extracted fields so downstream stages (normalize, validate,
// dedup, canonical) don't need to re-read the raw page from R2 on
// the happy path.
//
// struct tags:
//   - `json`   — wire format (Frame pub/sub)
//   - `parquet` — columnar layout (pkg/eventlog writer)
//
// Keep names identical between the two so operators grepping Parquet
// columns against pub/sub logs see matching identifiers.
type VariantIngestedV1 struct {
	VariantID  string `json:"variant_id"  parquet:"variant_id"`
	SourceID   string `json:"source_id"   parquet:"source_id"`
	ExternalID string `json:"external_id" parquet:"external_id"`
	HardKey    string `json:"hard_key"    parquet:"hard_key"`
	Stage      string `json:"stage"       parquet:"stage"`

	Title          string    `json:"title"           parquet:"title,optional"`
	Company        string    `json:"company"         parquet:"company,optional"`
	LocationText   string    `json:"location_text"   parquet:"location_text,optional"`
	Country        string    `json:"country"         parquet:"country,optional"`
	Language       string    `json:"language"        parquet:"language,optional"`
	RemoteType     string    `json:"remote_type"     parquet:"remote_type,optional"`
	EmploymentType string    `json:"employment_type" parquet:"employment_type,optional"`
	SalaryMin      float64   `json:"salary_min"      parquet:"salary_min,optional"`
	SalaryMax      float64   `json:"salary_max"      parquet:"salary_max,optional"`
	Currency       string    `json:"currency"        parquet:"currency,optional"`
	Description    string    `json:"description"     parquet:"description,optional"`
	ApplyURL       string    `json:"apply_url"       parquet:"apply_url,optional"`
	PostedAt       time.Time `json:"posted_at"       parquet:"posted_at,optional"`
	ScrapedAt      time.Time `json:"scraped_at"      parquet:"scraped_at"`
	ContentHash    string    `json:"content_hash"    parquet:"content_hash,optional"`

	RawArchiveRef       string `json:"raw_archive_ref"       parquet:"raw_archive_ref,optional"`
	ModelVersionExtract string `json:"model_version_extract" parquet:"model_version_extract,optional"`
}
