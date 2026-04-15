package dedupe

import (
	"context"
	"fmt"
	"time"

	"stawi.jobs/internal/domain"
)

type Engine struct {
	store domain.JobStore
}

func New(store domain.JobStore) *Engine {
	return &Engine{store: store}
}

func (e *Engine) UpsertAndCluster(ctx context.Context, variant domain.JobVariant) (domain.CanonicalJob, error) {
	saved, err := e.store.UpsertVariant(ctx, variant)
	if err != nil {
		return domain.CanonicalJob{}, fmt.Errorf("save variant: %w", err)
	}
	hardKey := domain.BuildHardKey(saved.Company, saved.Title, saved.LocationText, saved.ExternalJobID)
	existing, err := e.store.GetVariantByHardKey(ctx, hardKey)
	if err != nil {
		return domain.CanonicalJob{}, fmt.Errorf("lookup variant by hard key: %w", err)
	}
	if existing == nil || existing.ID == saved.ID {
		cluster, err := e.store.CreateCluster(ctx, saved.ID, 1.0)
		if err != nil {
			return domain.CanonicalJob{}, fmt.Errorf("create cluster: %w", err)
		}
		if err := e.store.BindVariantToCluster(ctx, saved.ID, cluster.ID, "hard", 1.0); err != nil {
			return domain.CanonicalJob{}, fmt.Errorf("bind cluster: %w", err)
		}
		canonical := canonicalFromVariant(cluster.ID, saved)
		if err := e.store.UpdateCanonicalJob(ctx, canonical); err != nil {
			return domain.CanonicalJob{}, fmt.Errorf("upsert canonical: %w", err)
		}
		return canonical, nil
	}
	cluster, err := e.store.CreateCluster(ctx, existing.ID, 0.98)
	if err != nil {
		return domain.CanonicalJob{}, fmt.Errorf("create matched cluster: %w", err)
	}
	if err := e.store.BindVariantToCluster(ctx, saved.ID, cluster.ID, "hard", 1.0); err != nil {
		return domain.CanonicalJob{}, fmt.Errorf("bind matched variant: %w", err)
	}
	canonical := canonicalFromVariant(cluster.ID, saved)
	if err := e.store.UpdateCanonicalJob(ctx, canonical); err != nil {
		return domain.CanonicalJob{}, fmt.Errorf("upsert canonical matched: %w", err)
	}
	return canonical, nil
}

func canonicalFromVariant(clusterID int64, v domain.JobVariant) domain.CanonicalJob {
	now := time.Now().UTC()
	return domain.CanonicalJob{
		ClusterID:      clusterID,
		Title:          v.Title,
		Company:        v.Company,
		Description:    v.Description,
		LocationText:   v.LocationText,
		Country:        v.Country,
		RemoteType:     v.RemoteType,
		EmploymentType: v.EmploymentType,
		SalaryMin:      v.SalaryMin,
		SalaryMax:      v.SalaryMax,
		Currency:       v.Currency,
		ApplyURL:       v.ApplyURL,
		PostedAt:       v.PostedAt,
		FirstSeenAt:    now,
		LastSeenAt:     now,
		IsActive:       true,
	}
}
