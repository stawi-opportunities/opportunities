// Package jobqueue implements the PostgreSQL-backed ingestion queue and the
// atomic canonical-opportunity write path.
package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"
)

type DBFunc func(context.Context, bool) *gorm.DB

type Store struct {
	db         DBFunc
	maxPending int64
}

func New(db DBFunc) *Store { return &Store{db: db} }

// NewProducer adds a hard queue-credit ceiling for crawler processes.
func NewProducer(db DBFunc, maxPending int64) *Store {
	return &Store{db: db, maxPending: maxPending}
}

var ErrCapacity = errors.New("jobqueue: pending capacity exhausted")

type EnqueueRequest struct {
	VariantID, SourceID, CrawlRunID, CrawlJobID string
	IdempotencyKey                                 string
	Payload                                        []byte
}

// Enqueue durably records work. A repeated crawl-page delivery is idempotent.
func (s *Store) Enqueue(ctx context.Context, r EnqueueRequest) error {
	if s == nil || s.db == nil {
		return errors.New("jobqueue: database is not configured")
	}
	if r.VariantID == "" || r.SourceID == "" || r.IdempotencyKey == "" || !json.Valid(r.Payload) {
		return errors.New("jobqueue: invalid enqueue request")
	}
	id := xid.New().String()
	return s.db(ctx, false).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if s.maxPending > 0 {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('job_ingest_queue_capacity'))`).Error; err != nil {
				return err
			}
			var exists bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM job_ingest_queue WHERE idempotency_key=?)`, r.IdempotencyKey).Scan(&exists).Error; err != nil {
				return err
			}
			if exists {
				return nil
			}
			var pending int64
			if err := tx.Raw(`SELECT count(*) FROM job_ingest_queue WHERE status IN ('pending','retry','processing')`).Scan(&pending).Error; err != nil {
				return err
			}
			if pending >= s.maxPending {
				return ErrCapacity
			}
		}
		result := tx.Exec(`INSERT INTO job_ingest_queue
			(id, variant_id, source_id, crawl_run_id, crawl_job_id, idempotency_key, payload)
			VALUES (?, ?, ?, NULLIF(?,''), NULLIF(?,''), ?, ?::jsonb)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			id, r.VariantID, r.SourceID, r.CrawlRunID, r.CrawlJobID, r.IdempotencyKey, string(r.Payload))
		if result.Error != nil {
			return fmt.Errorf("enqueue job: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return appendEvent(tx, id, r.VariantID, r.SourceID, "enqueued", 0, `{}`)
	})
}

// RecordRejected appends an immutable audit fact for a record rejected before
// it became queue work.
func (s *Store) RecordRejected(ctx context.Context, variantID, sourceID string, details any) error {
	if s == nil || s.db == nil {
		return errors.New("jobqueue: database is not configured")
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return appendEvent(s.db(ctx, false).WithContext(ctx), xid.New().String(), variantID, sourceID, "rejected", 0, string(b))
}

type Item struct {
	ID, VariantID, SourceID, CrawlRunID, CrawlJobID string
	Payload                                              []byte
	Attempt                                              int
}

// Claim leases disjoint work across replicas and reclaims expired leases.
func (s *Store) Claim(ctx context.Context, owner string, limit int, lease time.Duration) ([]Item, error) {
	if limit <= 0 {
		limit = 100
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	var rows []Item
	err := s.db(ctx, false).WithContext(ctx).Raw(`WITH picked AS (
		SELECT id FROM job_ingest_queue
		WHERE ((status IN ('pending','retry') AND available_at <= now())
		    OR (status = 'processing' AND lease_expires_at < now()))
		ORDER BY available_at, created_at
		LIMIT ? FOR UPDATE SKIP LOCKED
	)
	UPDATE job_ingest_queue q SET status='processing', attempt=q.attempt+1,
		claimed_at=now(), lease_expires_at=now()+make_interval(secs => ?),
		claimed_by=?, updated_at=now()
	FROM picked WHERE q.id=picked.id
	RETURNING q.id, q.variant_id, q.source_id,
		COALESCE(q.crawl_run_id,''), COALESCE(q.crawl_job_id,''),
		q.payload::text AS payload, q.attempt`, limit, lease.Seconds(), owner).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	return rows, nil
}

type Stats struct {
	Pending   int64
	OldestAge time.Duration
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var row struct {
		Pending       int64
		OldestSeconds float64
	}
	err := s.db(ctx, true).WithContext(ctx).Raw(`SELECT count(*) AS pending,
		COALESCE(EXTRACT(epoch FROM now()-min(created_at)),0) AS oldest_seconds
		FROM job_ingest_queue WHERE status IN ('pending','retry','processing')`).Scan(&row).Error
	return Stats{Pending: row.Pending, OldestAge: time.Duration(row.OldestSeconds * float64(time.Second))}, err
}

// DeactivateSource removes one source's lineage and hides canonicals that no
// longer have any active source behind them.
func (s *Store) DeactivateSource(ctx context.Context, sourceID, reason string) error {
	return s.db(ctx, false).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE opportunity_sources SET active=false WHERE source_id=?`, sourceID).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE opportunities o SET hidden=true, hidden_reason=?, updated_at=now()
			WHERE EXISTS (SELECT 1 FROM opportunity_sources os WHERE os.canonical_id=o.canonical_id AND os.source_id=? )
			AND NOT EXISTS (SELECT 1 FROM opportunity_sources active WHERE active.canonical_id=o.canonical_id AND active.active=true)`, reason, sourceID).Error
	})
}

type Canonical struct {
	CandidateID, HardKey, Kind, SourceID, ExternalID, Title string
	Description, IssuingEntity, Country, Region, City       string
	ApplyURL, Currency, EmploymentType, Seniority, GeoScope string
	Remote                                                  bool
	AmountMin, AmountMax                                    float64
	PostedAt, Deadline                                      *time.Time
	SeenAt                                                  time.Time
	Attributes                                              map[string]any
}

// Complete resolves identity, merges the serving row and source lineage, and
// acknowledges queue work in one database transaction.
func (s *Store) Complete(ctx context.Context, item Item, c Canonical) (string, error) {
	if c.CandidateID == "" || c.HardKey == "" || c.Kind == "" || c.Title == "" || c.ApplyURL == "" {
		return "", errors.New("jobqueue: canonical record misses required fields")
	}
	attrs, err := json.Marshal(c.Attributes)
	if err != nil {
		return "", fmt.Errorf("marshal attributes: %w", err)
	}
	if c.SeenAt.IsZero() {
		c.SeenAt = time.Now().UTC()
	}
	var canonicalID string
	err = s.db(ctx, false).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`INSERT INTO opportunity_identities (hard_key, canonical_id) VALUES (?, ?)
			ON CONFLICT (hard_key) DO UPDATE SET hard_key=EXCLUDED.hard_key
			RETURNING canonical_id`, c.HardKey, c.CandidateID).Scan(&canonicalID).Error; err != nil {
			return fmt.Errorf("resolve identity: %w", err)
		}
		slug := makeSlug(c.Kind, c.Title, c.IssuingEntity, canonicalID)
		if err := tx.Exec(`INSERT INTO opportunities (
			canonical_id, slug, kind, source_id, title, description, issuing_entity,
			country, region, city, remote, apply_url, posted_at, deadline, currency,
			amount_min, amount_max, employment_type, seniority, geo_scope, status,
			first_seen_at, last_seen_at, attributes, hidden)
		VALUES (?, ?, ?, ?, ?, NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), NULLIF(?,''),
			NULLIF(?,''), ?, ?, ?, ?, NULLIF(?,''), NULLIF(?,0), NULLIF(?,0),
			NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), 'active', ?, ?, ?::jsonb, false)
		ON CONFLICT (canonical_id) DO UPDATE SET
			title=COALESCE(NULLIF(EXCLUDED.title,''),opportunities.title),
			description=CASE WHEN length(COALESCE(EXCLUDED.description,'')) > length(COALESCE(opportunities.description,'')) THEN EXCLUDED.description ELSE opportunities.description END,
			issuing_entity=COALESCE(EXCLUDED.issuing_entity,opportunities.issuing_entity),
			country=COALESCE(EXCLUDED.country,opportunities.country), region=COALESCE(EXCLUDED.region,opportunities.region),
			city=COALESCE(EXCLUDED.city,opportunities.city), remote=EXCLUDED.remote,
			apply_url=EXCLUDED.apply_url, posted_at=COALESCE(EXCLUDED.posted_at,opportunities.posted_at),
			deadline=COALESCE(EXCLUDED.deadline,opportunities.deadline), currency=COALESCE(EXCLUDED.currency,opportunities.currency),
			amount_min=GREATEST(COALESCE(EXCLUDED.amount_min,0),COALESCE(opportunities.amount_min,0)),
			amount_max=GREATEST(COALESCE(EXCLUDED.amount_max,0),COALESCE(opportunities.amount_max,0)),
			employment_type=COALESCE(EXCLUDED.employment_type,opportunities.employment_type),
			seniority=COALESCE(EXCLUDED.seniority,opportunities.seniority), geo_scope=COALESCE(EXCLUDED.geo_scope,opportunities.geo_scope),
			last_seen_at=GREATEST(EXCLUDED.last_seen_at,opportunities.last_seen_at), status='active',
			attributes=opportunities.attributes || EXCLUDED.attributes, hidden=false, hidden_reason=NULL, updated_at=now()`,
			canonicalID, slug, c.Kind, c.SourceID, c.Title, c.Description, c.IssuingEntity,
			c.Country, c.Region, c.City, c.Remote, c.ApplyURL, c.PostedAt, c.Deadline, c.Currency,
			c.AmountMin, c.AmountMax, c.EmploymentType, c.Seniority, c.GeoScope,
			c.SeenAt, c.SeenAt, string(attrs)).Error; err != nil {
			return fmt.Errorf("upsert opportunity: %w", err)
		}
		contentHash, _ := c.Attributes["content_hash"].(string)
		if err := tx.Exec(`INSERT INTO opportunity_sources
			(canonical_id, source_id, external_id, apply_url, content_hash, first_seen_at, last_seen_at, last_crawl_run_id, inactive_after, active)
			VALUES (?, ?, ?, ?, NULLIF(?,''), ?, ?, NULLIF(?,''), NULL, true)
			ON CONFLICT (canonical_id,source_id,external_id) DO UPDATE SET
			apply_url=EXCLUDED.apply_url,
			content_hash=COALESCE(EXCLUDED.content_hash,opportunity_sources.content_hash),
			last_seen_at=GREATEST(EXCLUDED.last_seen_at,opportunity_sources.last_seen_at),
			last_crawl_run_id=CASE WHEN EXCLUDED.last_seen_at >= opportunity_sources.last_seen_at THEN EXCLUDED.last_crawl_run_id ELSE opportunity_sources.last_crawl_run_id END,
			active=CASE WHEN EXCLUDED.last_seen_at >= COALESCE(opportunity_sources.inactive_after,'-infinity'::timestamptz) THEN true ELSE opportunity_sources.active END,
			inactive_after=CASE WHEN EXCLUDED.last_seen_at >= COALESCE(opportunity_sources.inactive_after,'-infinity'::timestamptz) THEN NULL ELSE opportunity_sources.inactive_after END`,
			canonicalID, c.SourceID, c.ExternalID, c.ApplyURL, contentHash, c.SeenAt, c.SeenAt, item.CrawlRunID).Error; err != nil {
			return fmt.Errorf("upsert source lineage: %w", err)
		}
		if err := tx.Exec(`UPDATE opportunities o SET
			hidden=NOT EXISTS (SELECT 1 FROM opportunity_sources os WHERE os.canonical_id=o.canonical_id AND os.active=true),
			hidden_reason=CASE WHEN EXISTS (SELECT 1 FROM opportunity_sources os WHERE os.canonical_id=o.canonical_id AND os.active=true)
				THEN NULL ELSE 'not observed in latest full crawl' END
			WHERE o.canonical_id=?`, canonicalID).Error; err != nil {
			return fmt.Errorf("refresh opportunity visibility: %w", err)
		}
		result := tx.Exec(`UPDATE job_ingest_queue SET status='processed', processed_at=now(),
			lease_expires_at=NULL, claimed_by=NULL, last_error=NULL, updated_at=now()
			WHERE id=? AND status='processing'`, item.ID)
		if result.Error != nil || result.RowsAffected != 1 {
			return fmt.Errorf("complete queue item: affected=%d: %w", result.RowsAffected, result.Error)
		}
		return appendEvent(tx, item.ID, item.VariantID, item.SourceID, "processed", item.Attempt,
			fmt.Sprintf(`{"canonical_id":%q}`, canonicalID))
	})
	return canonicalID, err
}

// ReconcileSource marks listings not observed in a completed full crawl as
// inactive, then hides canonicals with no remaining active source.
func (s *Store) ReconcileSource(ctx context.Context, sourceID string, crawlStartedAt time.Time) error {
	return s.db(ctx, false).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE opportunity_sources SET active=false, inactive_after=?
			WHERE source_id=? AND last_seen_at < ?`, crawlStartedAt, sourceID, crawlStartedAt).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE opportunities o SET hidden=true, hidden_reason='not observed in latest full crawl', updated_at=now()
			WHERE EXISTS (SELECT 1 FROM opportunity_sources os WHERE os.canonical_id=o.canonical_id AND os.source_id=?)
			AND NOT EXISTS (SELECT 1 FROM opportunity_sources active WHERE active.canonical_id=o.canonical_id AND active.active=true)`, sourceID).Error
	})
}

func (s *Store) Retry(ctx context.Context, item Item, cause error, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	state, event := "retry", "retry_scheduled"
	if item.Attempt >= maxAttempts {
		state, event = "dead", "dead"
	}
	delay := math.Min(math.Pow(2, float64(item.Attempt)), 900)
	msg := cause.Error()
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	return s.db(ctx, false).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE job_ingest_queue SET status=?, available_at=now()+make_interval(secs=>?),
			lease_expires_at=NULL, claimed_by=NULL, last_error=?, updated_at=now() WHERE id=?`,
			state, delay, msg, item.ID).Error; err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{"error": msg})
		return appendEvent(tx, item.ID, item.VariantID, item.SourceID, event, item.Attempt, string(details))
	})
}

func appendEvent(tx *gorm.DB, ingestID, variantID, sourceID, eventType string, attempt int, details string) error {
	return tx.Exec(`INSERT INTO job_ingest_events
		(event_id, ingest_id, variant_id, source_id, event_type, attempt, details)
		VALUES (?, ?, ?, ?, ?, ?, ?::jsonb)`, xid.New().String(), ingestID, variantID, sourceID, eventType, attempt, details).Error
}
