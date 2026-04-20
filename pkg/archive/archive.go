// pkg/archive/archive.go
package archive

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Get* methods when the object is absent.
// Callers treat this as a logical miss rather than an error — missing
// raw for a known hash means something in the pipeline skipped the
// PutRaw step and needs re-triggering.
var ErrNotFound = errors.New("archive: object not found")

// Archive is the narrow interface the pipeline uses to talk to R2.
// Implemented in r2.go (production) and testing.go (in-memory fake).
//
// Method semantics:
//
//   PutRaw is content-addressed; the returned hash is sha256(body)
//   hex-encoded. Body is stored gzipped under raw/{hash}.html.gz.
//   Repeated calls with the same body are idempotent — the blob is
//   dedup'd server-side by checking HasRaw first, but implementations
//   MAY skip the check and rely on idempotent PUT.
//
//   Get* methods return ErrNotFound for missing objects. Any other
//   error indicates an infrastructure problem worth retrying.
type Archive interface {
	PutRaw(ctx context.Context, body []byte) (hash string, size int64, err error)
	GetRaw(ctx context.Context, hash string) ([]byte, error)
	HasRaw(ctx context.Context, hash string) (bool, error)

	PutCanonical(ctx context.Context, clusterID string, snap CanonicalSnapshot) error
	GetCanonical(ctx context.Context, clusterID string) (CanonicalSnapshot, error)

	PutVariant(ctx context.Context, clusterID, variantID string, v VariantBlob) error
	GetVariant(ctx context.Context, clusterID, variantID string) (VariantBlob, error)

	PutManifest(ctx context.Context, clusterID string, m Manifest) error
	GetManifest(ctx context.Context, clusterID string) (Manifest, error)

	// DeleteCluster removes every object under clusters/{clusterID}/.
	// Used by the purge sweeper when a canonical flips to 'deleted'.
	// DOES NOT delete raw/* blobs — that's ref-counted separately.
	DeleteCluster(ctx context.Context, clusterID string) error

	// DeleteRaw removes a single raw/{hash}.html.gz. Callers MUST
	// check the raw_refs table for ref count == 0 before invoking.
	DeleteRaw(ctx context.Context, hash string) error
}

// CanonicalSnapshot mirrors the fields of domain.CanonicalJob that
// are worth preserving in R2 as an audit trail. Any field that
// could be reconstructed from other tables is omitted to keep the
// snapshot small.
type CanonicalSnapshot struct {
	ID             string     `json:"id"`
	ClusterID      string     `json:"cluster_id"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	Company        string     `json:"company"`
	Description    string     `json:"description"`
	LocationText   string     `json:"location_text"`
	Country        string     `json:"country"`
	Language       string     `json:"language"`
	RemoteType     string     `json:"remote_type"`
	EmploymentType string     `json:"employment_type"`
	SalaryMin      float64    `json:"salary_min"`
	SalaryMax      float64    `json:"salary_max"`
	Currency       string     `json:"currency"`
	ApplyURL       string     `json:"apply_url"`
	QualityScore   float64    `json:"quality_score"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	Status         string     `json:"status"`
	Category       string     `json:"category"`
	R2Version      int        `json:"r2_version"`
	WrittenAt      time.Time  `json:"written_at"`
}

// VariantBlob is the per-variant processing record: enough to
// re-run extraction (raw_sha256 → archive.GetRaw) or to diff two
// variants side-by-side during quality review.
type VariantBlob struct {
	ID              string            `json:"id"`
	ClusterID       string            `json:"cluster_id"`
	SourceID        string            `json:"source_id"`
	SourceURL       string            `json:"source_url"`
	ApplyURL        string            `json:"apply_url"`
	RawContentHash  string            `json:"raw_content_hash"`
	CleanHTML       string            `json:"clean_html"`
	Markdown        string            `json:"markdown"`
	ExtractedFields map[string]any    `json:"extracted_fields,omitempty"`
	ScrapedAt       time.Time         `json:"scraped_at"`
	Stage           string            `json:"stage"`
	WrittenAt       time.Time         `json:"written_at"`
}

// Manifest is the mutable cluster index — rewritten on every
// variant or canonical write. DB is source-of-truth for current
// state; the manifest exists so `aws s3 sync clusters/{id}/` on
// its own gives a human a complete picture.
type Manifest struct {
	ClusterID   string            `json:"cluster_id"`
	CanonicalID string            `json:"canonical_id"`
	Slug        string            `json:"slug"`
	Variants    []ManifestVariant `json:"variants"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type ManifestVariant struct {
	VariantID      string    `json:"variant_id"`
	SourceID       string    `json:"source_id"`
	RawContentHash string    `json:"raw_content_hash"`
	ScrapedAt      time.Time `json:"scraped_at"`
}
