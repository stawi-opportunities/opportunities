package dedupe

import (
	"context"
	"time"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
)

// Engine orchestrates variant upsert, cluster creation, and canonical job
// upsert for a single incoming job posting.
type Engine struct {
	jobRepo *repository.JobRepository
}

// NewEngine creates a new dedupe Engine backed by the given JobRepository.
func NewEngine(jobRepo *repository.JobRepository) *Engine {
	return &Engine{jobRepo: jobRepo}
}

// UpsertAndCluster persists a JobVariant, groups it into a cluster, and
// returns the resulting CanonicalJob.
//
// Steps:
//  1. UpsertVariant — persist the incoming variant.
//  2. FindByHardKey — check whether this hard key already existed.
//  3. Set confidence: 1.0 for a brand-new key, 0.98 for an existing match.
//  4. CreateCluster with that confidence.
//  5. AddClusterMember with match_type="hard".
//  6. Build CanonicalJob from the variant (FirstSeenAt=now, LastSeenAt=now,
//     IsActive=true).
//  7. UpsertCanonical — persist the canonical record.
//  8. Return the canonical job.
func (e *Engine) UpsertAndCluster(ctx context.Context, variant *domain.JobVariant) (*domain.CanonicalJob, error) {
	// 1. Persist the variant (insert or update).
	if err := e.jobRepo.UpsertVariant(ctx, variant); err != nil {
		return nil, err
	}

	// 2. Look up the variant by its hard key to detect pre-existing matches.
	existing, err := e.jobRepo.FindByHardKey(ctx, variant.HardKey)
	if err != nil {
		return nil, err
	}

	// 3. Confidence is slightly lower when we merge into an existing record.
	confidence := 1.0
	if existing != nil && existing.ID != variant.ID {
		confidence = 0.98
	}

	// 4. Create a cluster anchored on this variant.
	cluster := &domain.JobCluster{
		CanonicalVariantID: variant.ID,
		Confidence:         confidence,
	}
	if err := e.jobRepo.CreateCluster(ctx, cluster); err != nil {
		return nil, err
	}

	// 5. Link the variant into the cluster.
	member := &domain.JobClusterMember{
		ClusterID: cluster.ID,
		VariantID: variant.ID,
		MatchType: "hard",
		Score:     confidence,
	}
	if err := e.jobRepo.AddClusterMember(ctx, member); err != nil {
		return nil, err
	}

	// 6. Build the canonical job from the variant.
	now := time.Now().UTC()
	canonical := &domain.CanonicalJob{
		ClusterID:      cluster.ID,
		Title:          variant.Title,
		Company:        variant.Company,
		Description:    variant.Description,
		LocationText:   variant.LocationText,
		Country:        variant.Country,
		RemoteType:     variant.RemoteType,
		EmploymentType: variant.EmploymentType,
		SalaryMin:      variant.SalaryMin,
		SalaryMax:      variant.SalaryMax,
		Currency:       variant.Currency,
		ApplyURL:       variant.ApplyURL,
		PostedAt:       variant.PostedAt,
		FirstSeenAt:    now,
		LastSeenAt:     now,
		IsActive:       true,
	}

	// 7. Persist the canonical record.
	if err := e.jobRepo.UpsertCanonical(ctx, canonical); err != nil {
		return nil, err
	}

	// 8. Return the canonical job.
	return canonical, nil
}
