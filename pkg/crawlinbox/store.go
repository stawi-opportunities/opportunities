// Package crawlinbox is the read/write API for the crawl_inbox table — a
// durable Postgres buffer between crawling and the NATS pipeline.
//
// The crawler/frontier Insert each freshly-crawled VariantIngestedV1 here
// instead of publishing straight to pl_ingested, so a crawl burst (one
// paginated board emits hundreds–thousands of variants at once) lands in the DB
// rather than slamming JetStream. A rate-limited pump ClaimBatch()es rows and
// publishes them to pl_ingested only while the queue is below high-water, then
// Delete()s the drained rows. The DB absorbs bursts; NATS holds only the
// working set.
package crawlinbox

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Store wraps the same read/write DB factory the rest of the codebase uses
// (frame.DatastoreManager().GetPool(...).DB).
type Store struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewStore wires the store.
func NewStore(db func(ctx context.Context, readOnly bool) *gorm.DB) *Store {
	return &Store{db: db}
}

// Row is a claimed inbox entry: the variant id plus its VariantIngestedV1
// envelope bytes, ready to publish onto pl_ingested.
type Row struct {
	VariantID string `gorm:"column:variant_id"`
	Payload   []byte `gorm:"column:payload"`
}

// Insert buffers a crawled variant. Idempotent on variant_id, so a re-crawl or
// a redelivered crawl request is a no-op rather than a duplicate.
func (s *Store) Insert(ctx context.Context, variantID, sourceID string, payload []byte) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db(ctx, false).WithContext(ctx).Exec(
		`INSERT INTO crawl_inbox (variant_id, source_id, payload)
		 VALUES (?, ?, ?)
		 ON CONFLICT (variant_id) DO NOTHING`,
		variantID, sourceID, payload,
	).Error
}

// ClaimBatch atomically claims up to limit rows that are unclaimed (or whose
// claim went stale — orphaned by a crashed pump older than staleAfter), marks
// them claimed, and returns their payloads. FOR UPDATE SKIP LOCKED lets
// multiple pump replicas claim disjoint batches without contention.
func (s *Store) ClaimBatch(ctx context.Context, limit int, staleAfter time.Duration) ([]Row, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var rows []Row
	err := s.db(ctx, false).WithContext(ctx).Raw(
		`UPDATE crawl_inbox SET claimed_at = now()
		   WHERE variant_id IN (
		     SELECT variant_id FROM crawl_inbox
		      WHERE claimed_at IS NULL
		         OR claimed_at < now() - make_interval(secs => ?)
		      ORDER BY created_at
		      LIMIT ?
		      FOR UPDATE SKIP LOCKED
		   )
		 RETURNING variant_id, payload`,
		staleAfter.Seconds(), limit,
	).Scan(&rows).Error
	return rows, err
}

// Delete removes successfully-pumped rows. Called after the variants are safely
// on pl_ingested (the durable NATS stream), so the buffer stays small.
func (s *Store) Delete(ctx context.Context, variantIDs []string) error {
	if s == nil || s.db == nil || len(variantIDs) == 0 {
		return nil
	}
	return s.db(ctx, false).WithContext(ctx).Exec(
		`DELETE FROM crawl_inbox WHERE variant_id IN ?`, variantIDs,
	).Error
}

// Depth returns the number of buffered (un-deleted) rows — buffer observability.
func (s *Store) Depth(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var n int64
	err := s.db(ctx, true).WithContext(ctx).Raw(`SELECT count(*) FROM crawl_inbox`).Scan(&n).Error
	return n, err
}
