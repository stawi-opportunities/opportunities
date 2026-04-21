package eventsv1

import "time"

// CanonicalUpsertedV1 is the event emitted by the canonical-merge
// stage once a cluster of variants has been merged into a single
// user-facing job row. Phase 2 consumes this event in the materializer
// to populate idx_jobs_rt — the Manticore index that backs search,
// browse, and detail.
//
// Fields mirror the design doc (§5.2 canonicals partition). Phase 2
// ships a minimum-viable set sufficient for BM25 search + country /
// remote_type / category filters + posted_at sort. Phases 3/4 add
// the extended intelligence fields (skills, required_skills, benefits,
// translated_langs, etc.) when the worker pipeline starts emitting
// them.
type CanonicalUpsertedV1 struct {
	CanonicalID    string    `json:"canonical_id"    parquet:"canonical_id"`
	ClusterID      string    `json:"cluster_id"      parquet:"cluster_id"`
	Slug           string    `json:"slug"            parquet:"slug"`
	Title          string    `json:"title"           parquet:"title,optional"`
	Company        string    `json:"company"         parquet:"company,optional"`
	Description    string    `json:"description"     parquet:"description,optional"`
	LocationText   string    `json:"location_text"   parquet:"location_text,optional"`
	Country        string    `json:"country"         parquet:"country,optional"`
	Language       string    `json:"language"        parquet:"language,optional"`
	RemoteType     string    `json:"remote_type"     parquet:"remote_type,optional"`
	EmploymentType string    `json:"employment_type" parquet:"employment_type,optional"`
	Seniority      string    `json:"seniority"       parquet:"seniority,optional"`
	SalaryMin      float64   `json:"salary_min"      parquet:"salary_min,optional"`
	SalaryMax      float64   `json:"salary_max"      parquet:"salary_max,optional"`
	Currency       string    `json:"currency"        parquet:"currency,optional"`
	Category       string    `json:"category"        parquet:"category,optional"`
	QualityScore   float64   `json:"quality_score"   parquet:"quality_score,optional"`
	Status         string    `json:"status"          parquet:"status"`
	PostedAt       time.Time `json:"posted_at"       parquet:"posted_at,optional"`
	FirstSeenAt    time.Time `json:"first_seen_at"   parquet:"first_seen_at,optional"`
	LastSeenAt     time.Time `json:"last_seen_at"    parquet:"last_seen_at,optional"`
	ExpiresAt      time.Time `json:"expires_at"      parquet:"expires_at,optional"`
	ApplyURL       string    `json:"apply_url"       parquet:"apply_url,optional"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}

// CanonicalExpiredV1 is emitted by the retention sweep when a
// canonical's apply link is determined dead or its expires_at has
// passed. The materializer flips status to 'expired' on idx_jobs_rt.
type CanonicalExpiredV1 struct {
	CanonicalID string    `json:"canonical_id" parquet:"canonical_id"`
	ClusterID   string    `json:"cluster_id"   parquet:"cluster_id,optional"`
	Reason      string    `json:"reason"       parquet:"reason,optional"`
	ExpiredAt   time.Time `json:"expired_at"   parquet:"expired_at"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}

// EmbeddingV1 is the event emitted by the embedder stage once a
// canonical job's semantic vector has been computed. Materializer
// updates the `embedding` HNSW attribute on idx_jobs_rt; Phase 3+
// adds hybrid BM25+KNN queries to /api/v2/search.
type EmbeddingV1 struct {
	CanonicalID  string    `json:"canonical_id"  parquet:"canonical_id"`
	Vector       []float32 `json:"vector"        parquet:"vector"`
	ModelVersion string    `json:"model_version" parquet:"model_version"`

	EventID    string    `json:"-" parquet:"event_id"`
	OccurredAt time.Time `json:"-" parquet:"occurred_at"`
}
