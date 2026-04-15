package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"stawi.jobs/internal/domain"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	if dsn == "" {
		return nil, errors.New("empty postgres dsn")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	cfg.MaxConns = 30
	cfg.MinConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() { s.pool.Close() }

func (s *PostgresStore) UpsertSource(ctx context.Context, src domain.Source) (domain.Source, error) {
	q := `
INSERT INTO sources (source_type, base_url, country, status, crawl_interval_sec, health_score, next_crawl_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (source_type, base_url)
DO UPDATE SET country = EXCLUDED.country, status = EXCLUDED.status, crawl_interval_sec = EXCLUDED.crawl_interval_sec,
              health_score = EXCLUDED.health_score, next_crawl_at = EXCLUDED.next_crawl_at, updated_at = now()
RETURNING id, source_type, base_url, country, status, crawl_interval_sec, health_score, last_seen_at, next_crawl_at, created_at, updated_at`
	row := s.pool.QueryRow(ctx, q, src.Type, src.BaseURL, src.Country, src.Status, src.CrawlIntervalSec, src.HealthScore, src.NextCrawlAt)
	if err := scanSource(row, &src); err != nil {
		return domain.Source{}, err
	}
	return src, nil
}

func (s *PostgresStore) ListDueSources(ctx context.Context, now time.Time, limit int) ([]domain.Source, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, source_type, base_url, country, status, crawl_interval_sec, health_score, last_seen_at, next_crawl_at, created_at, updated_at
FROM sources WHERE status = 'active' AND next_crawl_at <= $1 ORDER BY next_crawl_at ASC LIMIT $2`
	rows, err := s.pool.Query(ctx, q, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query due sources: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Source, 0)
	for rows.Next() {
		var src domain.Source
		if err := scanSource(rows, &src); err != nil {
			return nil, err
		}
		items = append(items, src)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListSources(ctx context.Context, limit int) ([]domain.Source, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, source_type, base_url, country, status, crawl_interval_sec, health_score, last_seen_at, next_crawl_at, created_at, updated_at
FROM sources ORDER BY id DESC LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Source, 0, limit)
	for rows.Next() {
		var src domain.Source
		if err := scanSource(rows, &src); err != nil {
			return nil, err
		}
		items = append(items, src)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetSource(ctx context.Context, id int64) (domain.Source, error) {
	q := `SELECT id, source_type, base_url, country, status, crawl_interval_sec, health_score, last_seen_at, next_crawl_at, created_at, updated_at
FROM sources WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	var src domain.Source
	if err := scanSource(row, &src); err != nil {
		return domain.Source{}, err
	}
	return src, nil
}

func (s *PostgresStore) TouchSource(ctx context.Context, id int64, next time.Time, health float64) error {
	q := `UPDATE sources SET last_seen_at = now(), next_crawl_at = $2, health_score = $3, updated_at = now() WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, next, health)
	if err != nil {
		return fmt.Errorf("touch source: %w", err)
	}
	return nil
}

func (s *PostgresStore) CreateCrawlJob(ctx context.Context, job domain.CrawlJob) (domain.CrawlJob, error) {
	q := `INSERT INTO crawl_jobs (source_id, scheduled_at, status, attempt, idempotency_key)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (idempotency_key) DO UPDATE SET source_id = EXCLUDED.source_id
RETURNING id, source_id, scheduled_at, started_at, finished_at, status, attempt, idempotency_key, error_code, created_at, updated_at`
	row := s.pool.QueryRow(ctx, q, job.SourceID, job.ScheduledAt, job.Status, job.Attempt, job.IdempotencyKey)
	var started, finished *time.Time
	if err := row.Scan(&job.ID, &job.SourceID, &job.ScheduledAt, &started, &finished, &job.Status, &job.Attempt, &job.IdempotencyKey, &job.ErrorCode, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return domain.CrawlJob{}, fmt.Errorf("create crawl job: %w", err)
	}
	job.StartedAt = started
	job.FinishedAt = finished
	return job, nil
}

func (s *PostgresStore) StartCrawlJob(ctx context.Context, id int64, startedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE crawl_jobs SET status='running', started_at=$2, updated_at=now() WHERE id=$1`, id, startedAt)
	if err != nil {
		return fmt.Errorf("start crawl job: %w", err)
	}
	return nil
}

func (s *PostgresStore) FinishCrawlJob(ctx context.Context, id int64, status domain.CrawlJobStatus, finishedAt time.Time, errCode string) error {
	_, err := s.pool.Exec(ctx, `UPDATE crawl_jobs SET status=$2, finished_at=$3, error_code=$4, updated_at=now() WHERE id=$1`, id, status, finishedAt, errCode)
	if err != nil {
		return fmt.Errorf("finish crawl job: %w", err)
	}
	return nil
}

func (s *PostgresStore) StoreRawPayload(ctx context.Context, payload domain.RawPayload) (domain.RawPayload, error) {
	q := `INSERT INTO raw_payloads (crawl_job_id, storage_uri, content_hash, fetched_at, http_status, body)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id`
	if err := s.pool.QueryRow(ctx, q, payload.CrawlJobID, payload.StorageURI, payload.ContentHash, payload.FetchedAt, payload.HTTPStatus, payload.Body).Scan(&payload.ID); err != nil {
		return domain.RawPayload{}, fmt.Errorf("store raw payload: %w", err)
	}
	return payload, nil
}

func (s *PostgresStore) UpsertVariant(ctx context.Context, v domain.JobVariant) (domain.JobVariant, error) {
	q := `INSERT INTO job_variants (external_job_id, source_id, source_url, apply_url, title, company, location_text, country, remote_type,
employment_type, salary_min, salary_max, currency, description_text, posted_at, scraped_at, content_hash, hard_key)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (source_id, external_job_id)
DO UPDATE SET source_url=EXCLUDED.source_url, apply_url=EXCLUDED.apply_url, title=EXCLUDED.title,
company=EXCLUDED.company, location_text=EXCLUDED.location_text, country=EXCLUDED.country, remote_type=EXCLUDED.remote_type,
employment_type=EXCLUDED.employment_type, salary_min=EXCLUDED.salary_min, salary_max=EXCLUDED.salary_max,
currency=EXCLUDED.currency, description_text=EXCLUDED.description_text, posted_at=EXCLUDED.posted_at,
scraped_at=EXCLUDED.scraped_at, content_hash=EXCLUDED.content_hash, hard_key=EXCLUDED.hard_key, updated_at=now()
RETURNING id`
	hardKey := domain.BuildHardKey(v.Company, v.Title, v.LocationText, v.ExternalJobID)
	if err := s.pool.QueryRow(ctx, q, v.ExternalJobID, v.SourceID, v.SourceURL, v.ApplyURL, v.Title, v.Company, v.LocationText, v.Country, v.RemoteType,
		v.EmploymentType, v.SalaryMin, v.SalaryMax, v.Currency, v.Description, v.PostedAt, v.ScrapedAt, v.ContentHash, hardKey).Scan(&v.ID); err != nil {
		return domain.JobVariant{}, fmt.Errorf("upsert variant: %w", err)
	}
	return v, nil
}

func (s *PostgresStore) GetVariantByHardKey(ctx context.Context, hardKey string) (*domain.JobVariant, error) {
	q := `SELECT id, external_job_id, source_id, source_url, apply_url, title, company, location_text, country, remote_type,
employment_type, salary_min, salary_max, currency, description_text, posted_at, scraped_at, content_hash
FROM job_variants WHERE hard_key = $1 LIMIT 1`
	row := s.pool.QueryRow(ctx, q, hardKey)
	v, err := scanVariant(row)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (s *PostgresStore) BindVariantToCluster(ctx context.Context, variantID, clusterID int64, matchType string, score float64) error {
	q := `INSERT INTO job_cluster_members (cluster_id, variant_id, match_type, score)
VALUES ($1,$2,$3,$4)
ON CONFLICT (cluster_id, variant_id)
DO UPDATE SET match_type=EXCLUDED.match_type, score=EXCLUDED.score, updated_at=now()`
	_, err := s.pool.Exec(ctx, q, clusterID, variantID, matchType, score)
	if err != nil {
		return fmt.Errorf("bind variant to cluster: %w", err)
	}
	return nil
}

func (s *PostgresStore) CreateCluster(ctx context.Context, canonicalVariantID int64, confidence float64) (domain.JobCluster, error) {
	c := domain.JobCluster{}
	q := `INSERT INTO job_clusters (canonical_variant_id, confidence) VALUES ($1,$2) RETURNING id, canonical_variant_id, confidence, updated_at`
	if err := s.pool.QueryRow(ctx, q, canonicalVariantID, confidence).Scan(&c.ID, &c.CanonicalVariantID, &c.Confidence, &c.UpdatedAt); err != nil {
		return domain.JobCluster{}, fmt.Errorf("create cluster: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) UpdateCanonicalJob(ctx context.Context, c domain.CanonicalJob) error {
	q := `INSERT INTO canonical_jobs (cluster_id, title, company, description_text, location_text, country, remote_type, employment_type,
salary_min, salary_max, currency, apply_url, posted_at, first_seen_at, last_seen_at, is_active)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (cluster_id)
DO UPDATE SET title=EXCLUDED.title, company=EXCLUDED.company, description_text=EXCLUDED.description_text,
location_text=EXCLUDED.location_text, country=EXCLUDED.country, remote_type=EXCLUDED.remote_type,
employment_type=EXCLUDED.employment_type, salary_min=EXCLUDED.salary_min, salary_max=EXCLUDED.salary_max,
currency=EXCLUDED.currency, apply_url=EXCLUDED.apply_url, posted_at=EXCLUDED.posted_at,
last_seen_at=EXCLUDED.last_seen_at, is_active=EXCLUDED.is_active, updated_at=now()`
	_, err := s.pool.Exec(ctx, q, c.ClusterID, c.Title, c.Company, c.Description, c.LocationText, c.Country, c.RemoteType, c.EmploymentType,
		c.SalaryMin, c.SalaryMax, c.Currency, c.ApplyURL, c.PostedAt, c.FirstSeenAt, c.LastSeenAt, c.IsActive)
	if err != nil {
		return fmt.Errorf("update canonical job: %w", err)
	}
	return nil
}

func (s *PostgresStore) SearchCanonicalJobs(ctx context.Context, query string, limit int) ([]domain.CanonicalJob, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT id, cluster_id, title, company, description_text, location_text, country, remote_type, employment_type,
salary_min, salary_max, currency, apply_url, posted_at, first_seen_at, last_seen_at, is_active
FROM canonical_jobs
WHERE $1 = '' OR to_tsvector('simple', title || ' ' || company || ' ' || description_text) @@ plainto_tsquery('simple', $1)
ORDER BY last_seen_at DESC
LIMIT $2`
	rows, err := s.pool.Query(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search canonical jobs: %w", err)
	}
	defer rows.Close()
	jobs := make([]domain.CanonicalJob, 0, limit)
	for rows.Next() {
		var c domain.CanonicalJob
		if err := rows.Scan(&c.ID, &c.ClusterID, &c.Title, &c.Company, &c.Description, &c.LocationText, &c.Country, &c.RemoteType, &c.EmploymentType,
			&c.SalaryMin, &c.SalaryMax, &c.Currency, &c.ApplyURL, &c.PostedAt, &c.FirstSeenAt, &c.LastSeenAt, &c.IsActive); err != nil {
			return nil, fmt.Errorf("scan canonical job: %w", err)
		}
		jobs = append(jobs, c)
	}
	return jobs, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanSource(row scanner, src *domain.Source) error {
	if err := row.Scan(&src.ID, &src.Type, &src.BaseURL, &src.Country, &src.Status, &src.CrawlIntervalSec, &src.HealthScore, &src.LastSeenAt, &src.NextCrawlAt, &src.CreatedAt, &src.UpdatedAt); err != nil {
		return fmt.Errorf("scan source: %w", err)
	}
	return nil
}

func scanVariant(row scanner) (domain.JobVariant, error) {
	var v domain.JobVariant
	if err := row.Scan(&v.ID, &v.ExternalJobID, &v.SourceID, &v.SourceURL, &v.ApplyURL, &v.Title, &v.Company, &v.LocationText, &v.Country, &v.RemoteType,
		&v.EmploymentType, &v.SalaryMin, &v.SalaryMax, &v.Currency, &v.Description, &v.PostedAt, &v.ScrapedAt, &v.ContentHash); err != nil {
		return domain.JobVariant{}, fmt.Errorf("scan variant: %w", err)
	}
	return v, nil
}

func isNoRows(err error) bool {
	return err != nil && errors.Is(err, pgx.ErrNoRows)
}
