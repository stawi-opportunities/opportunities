package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stawi.jobs/pkg/domain"
)

// JobRepository wraps GORM operations for job-related entities.
type JobRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewJobRepository creates a new JobRepository.
func NewJobRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *JobRepository {
	return &JobRepository{db: db}
}

// UpsertVariant inserts or updates a single job variant on conflict of
// (source_id, external_job_id).
func (r *JobRepository) UpsertVariant(ctx context.Context, v *domain.JobVariant) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "source_id"},
				{Name: "external_job_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"hard_key", "source_url", "apply_url", "title", "company",
				"location_text", "country", "remote_type", "employment_type",
				"salary_min", "salary_max", "currency", "description",
				"posted_at", "scraped_at", "content_hash", "updated_at",
			}),
		}).
		Create(v).Error
}

// UpsertVariants batch-upserts job variants in groups of 100.
func (r *JobRepository) UpsertVariants(ctx context.Context, variants []*domain.JobVariant) error {
	if len(variants) == 0 {
		return nil
	}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "source_id"},
				{Name: "external_job_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"hard_key", "source_url", "apply_url", "title", "company",
				"location_text", "country", "remote_type", "employment_type",
				"salary_min", "salary_max", "currency", "description",
				"posted_at", "scraped_at", "content_hash", "updated_at",
			}),
		}).
		CreateInBatches(variants, 100).Error
}

// FindByHardKey looks up a job variant by its deterministic hard key.
// Returns nil, nil when no record is found.
func (r *JobRepository) FindByHardKey(ctx context.Context, hardKey string) (*domain.JobVariant, error) {
	var v domain.JobVariant
	err := r.db(ctx, true).Where("hard_key = ?", hardKey).First(&v).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

// CreateCluster inserts a new job cluster.
func (r *JobRepository) CreateCluster(ctx context.Context, c *domain.JobCluster) error {
	return r.db(ctx, false).Create(c).Error
}

// AddClusterMember links a variant to a cluster, ignoring duplicate conflicts.
func (r *JobRepository) AddClusterMember(ctx context.Context, m *domain.JobClusterMember) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(m).Error
}

// UpsertCanonical inserts or updates a canonical job on conflict of cluster_id,
// then updates the tsvector search_vector column via raw SQL.
func (r *JobRepository) UpsertCanonical(ctx context.Context, cj *domain.CanonicalJob) error {
	db := r.db(ctx, false)
	if err := db.
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "cluster_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "company", "description", "location_text", "country",
				"remote_type", "employment_type", "salary_min", "salary_max",
				"currency", "apply_url", "seniority", "skills", "roles",
				"benefits", "contact_name", "contact_email", "department",
				"industry", "education", "experience", "deadline",
				"urgency_level", "hiring_timeline", "funnel_complexity",
				"company_size", "funding_stage", "required_skills",
				"nice_to_have_skills", "tools_frameworks", "geo_restrictions",
				"timezone_req", "application_type", "ats_platform",
				"role_scope", "quality_score",
				"posted_at", "last_seen_at", "is_active", "updated_at",
			}),
		}).
		Create(cj).Error; err != nil {
		return err
	}

	// Update the tsvector search_vector for full-text search.
	return db.Exec(`UPDATE canonical_jobs SET search_vector =
		setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
		setweight(to_tsvector('english', coalesce(company, '')), 'A') ||
		setweight(to_tsvector('english', coalesce(location_text, '')), 'B') ||
		setweight(to_tsvector('english', coalesce(description, '')), 'C')
		WHERE id = ?`, cj.ID).Error
}

// SearchCanonical performs a full-text search using tsvector/tsquery on active
// canonical jobs, ranked by ts_rank. Falls back to last_seen_at ordering when
// no query is provided.
func (r *JobRepository) SearchCanonical(ctx context.Context, query string, limit, offset int) ([]*domain.CanonicalJob, error) {
	var jobs []*domain.CanonicalJob
	if query != "" {
		err := r.db(ctx, true).
			Where("is_active = true AND search_vector @@ plainto_tsquery('english', ?)", query).
			Clauses(clause.OrderBy{
				Expression: clause.Expr{
					SQL:                "ts_rank(search_vector, plainto_tsquery('english', ?)) DESC",
					Vars:               []interface{}{query},
					WithoutParentheses: true,
				},
			}).
			Limit(limit).
			Offset(offset).
			Find(&jobs).Error
		return jobs, err
	}
	err := r.db(ctx, true).
		Where("is_active = true").
		Order("last_seen_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&jobs).Error
	return jobs, err
}

// UpdateEmbedding stores a JSON-encoded embedding vector for a canonical job.
func (r *JobRepository) UpdateEmbedding(ctx context.Context, canonicalID int64, embedding string) error {
	return r.db(ctx, false).
		Model(&domain.CanonicalJob{}).
		Where("id = ?", canonicalID).
		Update("embedding", embedding).Error
}

// ListMissingEmbeddings returns canonical jobs that have no embedding yet, limited
// to the given batch size. Used by the backfill loop to retry failed embeddings.
func (r *JobRepository) ListMissingEmbeddings(ctx context.Context, limit int) ([]*domain.CanonicalJob, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Where("is_active = true AND (embedding IS NULL OR embedding = '')").
		Order("id ASC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// CountVariants returns the total number of job variant records.
func (r *JobRepository) CountVariants(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.JobVariant{}).Count(&count).Error
	return count, err
}

// CountCanonical returns the total number of canonical job records.
func (r *JobRepository) CountCanonical(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).Model(&domain.CanonicalJob{}).Count(&count).Error
	return count, err
}

// UpdateQualityScore updates just the quality_score field of a canonical job.
func (r *JobRepository) UpdateQualityScore(ctx context.Context, id int64, score float64) error {
	return r.db(ctx, false).Model(&domain.CanonicalJob{}).Where("id = ?", id).Update("quality_score", score).Error
}

// TopByQualityScore returns active canonical jobs sorted by quality_score DESC,
// optionally filtered by a minimum score threshold.
func (r *JobRepository) TopByQualityScore(ctx context.Context, minScore float64, limit int) ([]*domain.CanonicalJob, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Where("is_active = true AND quality_score >= ?", minScore).
		Order("quality_score DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// CountVariantsByCountry returns variant counts grouped by country code.
func (r *JobRepository) CountVariantsByCountry(ctx context.Context) (map[string]int64, error) {
	type row struct {
		Country string
		Count   int64
	}
	var rows []row
	err := r.db(ctx, true).
		Model(&domain.JobVariant{}).
		Select("country, COUNT(*) as count").
		Group("country").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, r := range rows {
		result[r.Country] = r.Count
	}
	return result, nil
}

// GetPageState retrieves pagination state for a given source and page key.
// Returns nil, nil when no record is found.
func (r *JobRepository) GetPageState(ctx context.Context, sourceID int64, pageKey string) (*domain.CrawlPageState, error) {
	var ps domain.CrawlPageState
	err := r.db(ctx, true).
		Where("crawl_job_id = ? AND page_url = ?", sourceID, pageKey).
		First(&ps).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &ps, nil
}

// FindClusterByVariantID finds the cluster that a variant belongs to.
func (r *JobRepository) FindClusterByVariantID(ctx context.Context, variantID int64) (*domain.JobCluster, error) {
	var member domain.JobClusterMember
	err := r.db(ctx, true).Where("variant_id = ?", variantID).First(&member).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	var cluster domain.JobCluster
	err = r.db(ctx, true).Where("id = ?", member.ClusterID).First(&cluster).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &cluster, nil
}

// TruncateCanonicals deletes all canonical_jobs, job_clusters, and job_cluster_members.
func (r *JobRepository) TruncateCanonicals(ctx context.Context) error {
	db := r.db(ctx, false)
	if err := db.Exec("DELETE FROM job_cluster_members").Error; err != nil {
		return err
	}
	if err := db.Exec("DELETE FROM canonical_jobs").Error; err != nil {
		return err
	}
	return db.Exec("DELETE FROM job_clusters").Error
}

// ListAllVariants returns variants in batches for rebuild processing.
func (r *JobRepository) ListAllVariants(ctx context.Context, batchSize, offset int) ([]*domain.JobVariant, error) {
	var variants []*domain.JobVariant
	err := r.db(ctx, true).
		Order("scraped_at ASC, id ASC").
		Limit(batchSize).Offset(offset).
		Find(&variants).Error
	return variants, err
}

// UpsertPageState inserts or updates page state on conflict of
// (crawl_job_id, page_url), which map to source_id and page_key semantically.
func (r *JobRepository) UpsertPageState(ctx context.Context, ps *domain.CrawlPageState) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "crawl_job_id"},
				{Name: "page_url"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"page_num", "cursor_next", "fetched_at", "job_count",
			}),
		}).
		Create(ps).Error
}
