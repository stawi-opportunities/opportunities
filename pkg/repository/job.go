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

// UpsertCanonical inserts or updates a canonical job on conflict of cluster_id.
func (r *JobRepository) UpsertCanonical(ctx context.Context, cj *domain.CanonicalJob) error {
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "cluster_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "company", "description", "location_text", "country",
				"remote_type", "employment_type", "salary_min", "salary_max",
				"currency", "apply_url", "posted_at", "last_seen_at",
				"is_active", "updated_at",
			}),
		}).
		Create(cj).Error
}

// SearchCanonical performs a case-insensitive search against title, company,
// and description_text on active canonical jobs, ordered by last_seen_at DESC.
func (r *JobRepository) SearchCanonical(ctx context.Context, query string, limit, offset int) ([]*domain.CanonicalJob, error) {
	like := "%" + query + "%"
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Where(
			"is_active = true AND (title ILIKE ? OR company ILIKE ? OR description ILIKE ?)",
			like, like, like,
		).
		Order("last_seen_at DESC").
		Limit(limit).
		Offset(offset).
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
