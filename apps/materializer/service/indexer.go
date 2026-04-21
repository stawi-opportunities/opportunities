package service

import (
	"context"
	"fmt"
	"hash/fnv"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/searchindex"
)

// Indexer converts Parquet-decoded event rows into Manticore writes
// via the HTTP JSON API. Each public method corresponds to one
// collection (canonicals, embeddings). Methods are idempotent:
// using the same canonical_id → hashID as the Manticore row id
// makes /replace act as upsert.
type Indexer struct {
	client *searchindex.Client
}

// NewIndexer wraps a Manticore client.
func NewIndexer(c *searchindex.Client) *Indexer { return &Indexer{client: c} }

// ApplyCanonicalsParquet decodes a Parquet body of CanonicalUpsertedV1
// rows and issues one /replace per row.
func (i *Indexer) ApplyCanonicalsParquet(ctx context.Context, body []byte) (int, error) {
	rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
	if err != nil {
		return 0, fmt.Errorf("indexer: decode canonicals parquet: %w", err)
	}
	n := 0
	for _, r := range rows {
		if err := i.replaceOne(ctx, r); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// replaceOne issues a single /replace against idx_jobs_rt. The row
// id is a stable hash of canonical_id — Manticore requires a bigint
// pk and keying on hash(canonical_id) gives us idempotent upsert.
func (i *Indexer) replaceOne(ctx context.Context, r eventsv1.CanonicalUpsertedV1) error {
	doc := map[string]any{
		"canonical_id":    r.CanonicalID,
		"slug":            r.Slug,
		"title":           r.Title,
		"company":         r.Company,
		"description":     r.Description,
		"location_text":   r.LocationText,
		"category":        r.Category,
		"country":         r.Country,
		"language":        r.Language,
		"remote_type":     r.RemoteType,
		"employment_type": r.EmploymentType,
		"seniority":       r.Seniority,
		"salary_min":      uint64(r.SalaryMin),
		"salary_max":      uint64(r.SalaryMax),
		"currency":        r.Currency,
		"quality_score":   float32(r.QualityScore),
		"is_featured":     r.QualityScore >= 80,
		"posted_at":       r.PostedAt.Unix(),
		"last_seen_at":    r.LastSeenAt.Unix(),
		"expires_at":      r.ExpiresAt.Unix(),
		"status":          r.Status,
	}
	return i.client.Replace(ctx, "idx_jobs_rt", hashID(r.CanonicalID), doc)
}

// ApplyEmbeddingsParquet updates the `embedding` attribute on
// idx_jobs_rt rows by canonical_id. If the row doesn't exist yet
// (embeddings can arrive slightly ahead of canonical for a fresh
// job), Manticore's /update returns updated=0 and the call is
// a no-op — the next canonical event will land the base row, and
// the next embedding event for that canonical will re-apply.
func (i *Indexer) ApplyEmbeddingsParquet(ctx context.Context, body []byte) (int, error) {
	rows, err := eventlog.ReadParquet[eventsv1.EmbeddingV1](body)
	if err != nil {
		return 0, fmt.Errorf("indexer: decode embeddings parquet: %w", err)
	}
	n := 0
	for _, r := range rows {
		id := hashID(r.CanonicalID)
		doc := map[string]any{
			"embedding":       r.Vector,
			"embedding_model": r.ModelVersion,
		}
		if err := i.client.Update(ctx, "idx_jobs_rt", id, doc); err != nil {
			return n, fmt.Errorf("indexer: update embedding: %w", err)
		}
		n++
	}
	return n, nil
}

// hashID maps a canonical_id (xid) to a stable bigint for Manticore's
// primary key.
func hashID(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
