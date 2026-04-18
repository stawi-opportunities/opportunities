package dedupe

import (
	"context"
	"time"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/scoring"
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

// UpsertAndCluster persists a JobVariant, groups it into the correct cluster,
// and returns the resulting CanonicalJob.
//
// Steps:
//  1. UpsertVariant — persist the incoming variant.
//  2. FindByHardKey — check whether this hard key already existed.
//  3. If an existing variant is found (different record):
//     a. FindClusterByVariantID for the existing variant.
//     b. If a cluster exists → reuse that cluster ID.
//     c. If no cluster → create a new cluster anchored on the existing variant,
//     then add the new variant as a member.
//  4. Else (brand-new job): create a new cluster anchored on this variant and
//     add the variant as a member.
//  5. Build canonical job from variant with the resolved cluster ID.
//  6. Compute quality score.
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

	var clusterID int64

	if existing != nil && existing.ID != variant.ID {
		// 2a. Hard-key match on a different record — reuse or create its cluster.
		cluster, err := e.jobRepo.FindClusterByVariantID(ctx, existing.ID)
		if err != nil {
			return nil, err
		}

		if cluster != nil {
			// 3b. Reuse the existing cluster.
			clusterID = cluster.ID
		} else {
			// 3c. Existing variant has no cluster yet; create one anchored on it.
			cluster = &domain.JobCluster{
				CanonicalVariantID: existing.ID,
				Confidence:         0.98,
			}
			if err := e.jobRepo.CreateCluster(ctx, cluster); err != nil {
				return nil, err
			}
			clusterID = cluster.ID
		}

		// Add the new variant to the resolved cluster.
		member := &domain.JobClusterMember{
			ClusterID: clusterID,
			VariantID: variant.ID,
			MatchType: "hard",
			Score:     0.98,
		}
		if err := e.jobRepo.AddClusterMember(ctx, member); err != nil {
			return nil, err
		}
	} else {
		// 4. Brand-new job — create a fresh cluster anchored on this variant.
		cluster := &domain.JobCluster{
			CanonicalVariantID: variant.ID,
			Confidence:         1.0,
		}
		if err := e.jobRepo.CreateCluster(ctx, cluster); err != nil {
			return nil, err
		}
		clusterID = cluster.ID

		member := &domain.JobClusterMember{
			ClusterID: clusterID,
			VariantID: variant.ID,
			MatchType: "hard",
			Score:     1.0,
		}
		if err := e.jobRepo.AddClusterMember(ctx, member); err != nil {
			return nil, err
		}
	}

	// 5–6. Build canonical job and compute quality score.
	now := time.Now().UTC()
	canonical := buildCanonicalFromVariant(variant, clusterID, now)
	canonical.QualityScore = scoring.Score(canonical)

	// 7. Persist the canonical record.
	if err := e.jobRepo.UpsertCanonical(ctx, canonical); err != nil {
		return nil, err
	}

	// 8. Return the canonical job.
	return canonical, nil
}

// buildCanonicalFromVariant copies all fields from a variant into a new
// CanonicalJob bound to the given clusterID, with timestamps set to now.
func buildCanonicalFromVariant(variant *domain.JobVariant, clusterID int64, now time.Time) *domain.CanonicalJob {
	return &domain.CanonicalJob{
		ClusterID:        clusterID,
		Title:            variant.Title,
		Company:          variant.Company,
		Description:      variant.Description,
		LocationText:     variant.LocationText,
		Country:          variant.Country,
		Language:         variant.Language,
		RemoteType:       variant.RemoteType,
		EmploymentType:   variant.EmploymentType,
		SalaryMin:        variant.SalaryMin,
		SalaryMax:        variant.SalaryMax,
		Currency:         variant.Currency,
		ApplyURL:         variant.ApplyURL,
		Seniority:        variant.Seniority,
		Skills:           variant.Skills,
		Roles:            variant.Roles,
		Benefits:         variant.Benefits,
		ContactName:      variant.ContactName,
		ContactEmail:     variant.ContactEmail,
		Department:       variant.Department,
		Industry:         variant.Industry,
		Education:        variant.Education,
		Experience:       variant.Experience,
		Deadline:         variant.Deadline,
		UrgencyLevel:     variant.UrgencyLevel,
		HiringTimeline:   variant.HiringTimeline,
		FunnelComplexity: variant.FunnelComplexity,
		CompanySize:      variant.CompanySize,
		FundingStage:     variant.FundingStage,
		RequiredSkills:   variant.RequiredSkills,
		NiceToHaveSkills: variant.NiceToHaveSkills,
		ToolsFrameworks:  variant.ToolsFrameworks,
		GeoRestrictions:  variant.GeoRestrictions,
		TimezoneReq:      variant.TimezoneReq,
		ApplicationType:  variant.ApplicationType,
		ATSPlatform:      variant.ATSPlatform,
		RoleScope:        variant.RoleScope,
		PostedAt:         variant.PostedAt,
		FirstSeenAt:      now,
		LastSeenAt:       now,
		Status:           "active",
	}
}
