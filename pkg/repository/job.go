package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/scoring"
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
// search_vector is a GENERATED column in Postgres and populated automatically;
// status/expires_at/category are defaulted here if the caller didn't set them.
func (r *JobRepository) UpsertCanonical(ctx context.Context, cj *domain.CanonicalJob) error {
	if cj.Status == "" {
		cj.Status = "active"
	}
	if cj.ExpiresAt == nil {
		base := cj.FirstSeenAt
		if cj.PostedAt != nil {
			base = *cj.PostedAt
		}
		if base.IsZero() {
			base = time.Now()
		}
		exp := base.Add(120 * 24 * time.Hour)
		cj.ExpiresAt = &exp
	}
	if cj.Category == "" {
		cj.Category = string(domain.DeriveCategory(cj.Roles, cj.Industry))
	}
	return r.db(ctx, false).
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
				"posted_at", "last_seen_at", "status", "expires_at", "category",
				"updated_at",
			}),
		}).
		Create(cj).Error
}

// SearchRequest describes an FTS query with filters, pagination, and sort.
// Cursor fields are used only for empty-query keyset pagination; text-search
// (Query != "") uses Offset instead.
type SearchRequest struct {
	Query          string
	Category       string
	RemoteType     string
	EmploymentType string
	Seniority      string
	Country        string
	Sort           string // "relevance"|"recent"|"quality"|"salary_high"; defaults derived from Query
	Limit          int
	Offset         int
	CursorPostedAt *time.Time
	CursorID       int64
}

// SearchResult is the denormalized row returned to UI consumers. Snippet is a
// pre-truncated excerpt of the description so list pages don't ship full text.
type SearchResult struct {
	ID           int64      `json:"id"`
	Slug         string     `json:"slug"`
	Title        string     `json:"title"`
	Company      string     `json:"company"`
	LocationText string     `json:"location_text"`
	Country      string     `json:"country"`
	RemoteType   string     `json:"remote_type"`
	Category     string     `json:"category"`
	SalaryMin    float64    `json:"salary_min"`
	SalaryMax    float64    `json:"salary_max"`
	Currency     string     `json:"currency"`
	PostedAt     *time.Time `json:"posted_at"`
	QualityScore float64    `json:"quality_score"`
	Snippet      string     `json:"snippet"`
	IsFeatured   bool       `json:"is_featured"`
}

// SearchCanonical runs an FTS query with filters. See SearchRequest for the
// contract. Caller should request Limit+1 and infer has_more from overflow.
func (r *JobRepository) SearchCanonical(ctx context.Context, req SearchRequest) ([]*SearchResult, error) {
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 20
	}
	if req.Offset < 0 {
		req.Offset = 0
	}
	if req.Offset > 1000 {
		req.Offset = 1000
	}

	db := r.db(ctx, true).Table("canonical_jobs").
		Select(`id, slug, title, company, location_text, country, remote_type,
				category, salary_min, salary_max, currency, posted_at, quality_score,
				left(coalesce(description, ''), 200) AS snippet,
				(quality_score >= 80) AS is_featured`).
		Where("status = 'active'")

	if req.Category != "" {
		db = db.Where("category = ?", req.Category)
	}
	if req.RemoteType != "" {
		db = db.Where("remote_type = ?", req.RemoteType)
	}
	if req.EmploymentType != "" {
		db = db.Where("employment_type = ?", req.EmploymentType)
	}
	if req.Seniority != "" {
		db = db.Where("seniority = ?", req.Seniority)
	}
	if req.Country != "" {
		db = db.Where("country = ?", req.Country)
	}

	q := strings.TrimSpace(req.Query)
	if q != "" {
		db = db.Where("search_vector @@ plainto_tsquery('simple', ?)", q)
		switch req.Sort {
		case "recent":
			db = db.Order("posted_at DESC NULLS LAST").Order("id DESC")
		case "quality":
			db = db.Order("quality_score DESC").Order("posted_at DESC NULLS LAST").Order("id DESC")
		case "salary_high":
			db = db.Order("salary_max DESC NULLS LAST").Order("id DESC")
		default: // relevance
			db = db.Clauses(clause.OrderBy{
				Expression: clause.Expr{
					SQL:                "ts_rank_cd(search_vector, plainto_tsquery('simple', ?)) DESC",
					Vars:               []any{q},
					WithoutParentheses: true,
				},
			}).Order("posted_at DESC NULLS LAST").Order("id DESC")
		}
		db = db.Offset(req.Offset)
	} else {
		if req.CursorPostedAt != nil && req.CursorID > 0 {
			db = db.Where("(posted_at, id) < (?, ?)", *req.CursorPostedAt, req.CursorID)
		}
		switch req.Sort {
		case "quality":
			db = db.Order("quality_score DESC").Order("id DESC")
		case "salary_high":
			db = db.Order("salary_max DESC NULLS LAST").Order("id DESC")
		default: // recent
			db = db.Order("posted_at DESC NULLS LAST").Order("id DESC")
		}
	}

	var out []*SearchResult
	if err := db.Limit(req.Limit + 1).Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
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
		Where("status = 'active' AND (embedding IS NULL OR embedding = '')").
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

// CountCanonical returns the number of active canonical job records.
// Rows with status 'duplicate', 'deleted', 'expired', or 'inactive' are
// excluded so the home-page stats tile only counts what's actually
// visible to users.
func (r *JobRepository) CountCanonical(ctx context.Context) (int64, error) {
	var count int64
	err := r.db(ctx, true).
		Model(&domain.CanonicalJob{}).
		Where("status = 'active'").
		Count(&count).Error
	return count, err
}

// UpdateQualityScore updates just the quality_score field of a canonical job.
func (r *JobRepository) UpdateQualityScore(ctx context.Context, id int64, score float64) error {
	return r.db(ctx, false).Model(&domain.CanonicalJob{}).Where("id = ?", id).Update("quality_score", score).Error
}

// MarkPublished stamps published_at=now() and sets r2_version for a canonical job.
func (r *JobRepository) MarkPublished(ctx context.Context, canonicalJobID int64, nextVersion int) error {
	return r.db(ctx, false).
		Table("canonical_jobs").
		Where("id = ?", canonicalJobID).
		Updates(map[string]any{
			"published_at": gorm.Expr("now()"),
			"r2_version":   nextVersion,
		}).Error
}

// ClearPublished sets published_at to NULL. Used by the unpublish path.
func (r *JobRepository) ClearPublished(ctx context.Context, canonicalJobID int64) error {
	return r.db(ctx, false).
		Table("canonical_jobs").
		Where("id = ?", canonicalJobID).
		Update("published_at", nil).Error
}

// TopByQualityScore returns active canonical jobs sorted by quality_score DESC,
// optionally filtered by a minimum score threshold.
func (r *JobRepository) TopByQualityScore(ctx context.Context, minScore float64, limit int) ([]*domain.CanonicalJob, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Where("status = 'active' AND quality_score >= ?", minScore).
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

// TruncateCanonicals deletes all canonical_jobs, job_clusters, and job_cluster_members
// within a single transaction to maintain referential consistency.
func (r *JobRepository) TruncateCanonicals(ctx context.Context) error {
	return r.db(ctx, false).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM job_cluster_members").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM canonical_jobs").Error; err != nil {
			return err
		}
		return tx.Exec("DELETE FROM job_clusters").Error
	})
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

// FilterForCandidate returns active canonical jobs matching a candidate's hard
// filters (remote preference, salary floor, preferred countries), ordered by
// quality_score DESC.
func (r *JobRepository) FilterForCandidate(ctx context.Context, c *domain.CandidateProfile, limit int) ([]*domain.CanonicalJob, error) {
	q := r.db(ctx, true).Where("status = 'active'")

	if c.RemotePreference == "remote_only" {
		q = q.Where("remote_type = ?", "remote")
	}

	if c.SalaryMin > 0 {
		q = q.Where("salary_max >= ? OR salary_max = 0", c.SalaryMin)
	}

	if c.PreferredCountries != "" {
		parts := strings.Split(c.PreferredCountries, ",")
		countries := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				countries = append(countries, t)
			}
		}
		if len(countries) > 0 {
			q = q.Where("country IN ? OR country = '' OR country IS NULL", countries)
		}
	}

	var jobs []*domain.CanonicalJob
	err := q.Order("quality_score DESC").Limit(limit).Find(&jobs).Error
	return jobs, err
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

// UpdateCanonicalFields updates specific fields on a canonical job by ID.
func (r *JobRepository) UpdateCanonicalFields(ctx context.Context, id int64, updates map[string]any) error {
	return r.db(ctx, false).Model(&domain.CanonicalJob{}).Where("id = ?", id).Updates(updates).Error
}

// RecomputeQualityScore loads a canonical job, recomputes its score, and saves it.
func (r *JobRepository) RecomputeQualityScore(ctx context.Context, id int64) error {
	var job domain.CanonicalJob
	if err := r.db(ctx, true).Where("id = ?", id).First(&job).Error; err != nil {
		return err
	}
	score := scoring.Score(&job)
	return r.db(ctx, false).Model(&domain.CanonicalJob{}).Where("id = ?", id).Update("quality_score", score).Error
}

// ListUnenriched returns canonical jobs that have empty intelligence fields.
func (r *JobRepository) ListUnenriched(ctx context.Context, limit int) ([]*domain.CanonicalJob, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Where("status = 'active' AND (seniority IS NULL OR seniority = '') AND description != ''").
		Order("id ASC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// UpdateStage sets the pipeline stage for a variant.
func (r *JobRepository) UpdateStage(ctx context.Context, variantID int64, stage string) error {
	return r.db(ctx, false).Model(&domain.JobVariant{}).
		Where("id = ?", variantID).
		Update("stage", stage).Error
}

// UpdateStageWithContent sets stage and content fields together.
func (r *JobRepository) UpdateStageWithContent(ctx context.Context, variantID int64, stage string, rawHTML, cleanHTML, markdown string) error {
	return r.db(ctx, false).Model(&domain.JobVariant{}).
		Where("id = ?", variantID).
		Updates(map[string]any{
			"stage":      stage,
			"raw_html":   rawHTML,
			"clean_html": cleanHTML,
			"markdown":   markdown,
		}).Error
}

// UpdateValidation sets validation results on a variant.
func (r *JobRepository) UpdateValidation(ctx context.Context, variantID int64, stage string, score float64, notes string) error {
	return r.db(ctx, false).Model(&domain.JobVariant{}).
		Where("id = ?", variantID).
		Updates(map[string]any{
			"stage":            stage,
			"validation_score": score,
			"validation_notes": notes,
		}).Error
}

// ListByStage returns variants at a given pipeline stage.
func (r *JobRepository) ListByStage(ctx context.Context, stage string, limit int) ([]*domain.JobVariant, error) {
	var variants []*domain.JobVariant
	err := r.db(ctx, true).
		Where("stage = ?", stage).
		Order("id ASC").
		Limit(limit).
		Find(&variants).Error
	return variants, err
}

// GetCanonicalByID retrieves an active canonical job by primary key.
// Returns nil, nil when no record is found.
func (r *JobRepository) GetCanonicalByID(ctx context.Context, id int64) (*domain.CanonicalJob, error) {
	var j domain.CanonicalJob
	err := r.db(ctx, true).Where("id = ? AND status = 'active'", id).First(&j).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}

// ListActiveCanonical returns active canonical jobs above a quality threshold,
// ordered by posted_at DESC with pagination.
func (r *JobRepository) ListActiveCanonical(ctx context.Context, minQuality float64, limit, offset int) ([]*domain.CanonicalJob, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Where("status = 'active' AND quality_score >= ?", minQuality).
		Order("posted_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&jobs).Error
	return jobs, err
}

// CountByCategory returns counts of active canonical jobs grouped by derived category.
func (r *JobRepository) CountByCategory(ctx context.Context) (map[string]int64, error) {
	var jobs []*domain.CanonicalJob
	err := r.db(ctx, true).
		Select("roles, industry").
		Where("status = 'active'").
		Find(&jobs).Error
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int64)
	for _, j := range jobs {
		cat := string(domain.DeriveCategory(j.Roles, j.Industry))
		counts[cat]++
	}
	return counts, nil
}

// ListByStageAndSource returns variants at a given pipeline stage for a specific
// source, ordered by most-recent first and capped at limit rows.
func (r *JobRepository) ListByStageAndSource(ctx context.Context, sourceID int64, stage string, limit int) ([]*domain.JobVariant, error) {
	var variants []*domain.JobVariant
	err := r.db(ctx, true).
		Where("source_id = ? AND stage = ?", sourceID, stage).
		Order("id DESC").
		Limit(limit).
		Find(&variants).Error
	return variants, err
}

// GetVariantByID returns a single JobVariant by primary key.
// Returns nil, nil when no record is found.
func (r *JobRepository) GetVariantByID(ctx context.Context, id int64) (*domain.JobVariant, error) {
	var v domain.JobVariant
	err := r.db(ctx, true).Where("id = ?", id).First(&v).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

// UpdateVariantFields updates arbitrary fields on a job variant by ID.
func (r *JobRepository) UpdateVariantFields(ctx context.Context, id int64, updates map[string]any) error {
	return r.db(ctx, false).Model(&domain.JobVariant{}).Where("id = ?", id).Updates(updates).Error
}

// CountByStage returns the count of variants at each stage.
func (r *JobRepository) CountByStage(ctx context.Context) (map[string]int64, error) {
	type result struct {
		Stage string
		Count int64
	}
	var results []result
	err := r.db(ctx, true).
		Model(&domain.JobVariant{}).
		Select("stage, count(*) as count").
		Group("stage").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(results))
	for _, r := range results {
		m[r.Stage] = r.Count
	}
	return m, nil
}
