//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

func newSource(t *testing.T, g *gorm.DB, id string, status domain.SourceStatus) {
	t.Helper()
	require.NoError(t, g.Create(&domain.Source{
		BaseModel: domain.BaseModel{ID: id}, Type: domain.SourceGenericHTML,
		BaseURL: "https://" + id + ".example.com", Status: status,
	}).Error)
}

func statusOf(t *testing.T, g *gorm.DB, id string) domain.SourceStatus {
	t.Helper()
	var s string
	require.NoError(t, g.Raw(`SELECT status FROM sources WHERE id=?`, id).Scan(&s).Error)
	return domain.SourceStatus(s)
}

// No automatic writer may undo an operator stop.
func TestSourceStatus_AutomaticWritersNeverOverwriteStop(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	repo := repository.NewSourceRepository(func(context.Context, bool) *gorm.DB { return g })
	newSource(t, g, "stopped", domain.SourceDisabled)

	require.NoError(t, repo.RecordFailure(ctx, "stopped", 0.1, 3))
	require.Equal(t, domain.SourceDisabled, statusOf(t, g, "stopped"))
	require.NoError(t, repo.RecordFailure(ctx, "stopped", 0.0, 9))
	require.Equal(t, domain.SourceDisabled, statusOf(t, g, "stopped"))
	var failures int
	require.NoError(t, g.Raw(`SELECT consecutive_failures FROM sources WHERE id='stopped'`).Scan(&failures).Error)
	require.Equal(t, 9, failures, "health bookkeeping still recorded")

	changed, err := repo.TransitionStatus(ctx, "stopped", []domain.SourceStatus{domain.SourceActive, domain.SourceDegraded}, domain.SourcePaused)
	require.NoError(t, err)
	require.False(t, changed)

	require.NoError(t, repo.SaveVerificationReport(ctx, "stopped", &domain.VerificationReport{URLValid: true}, domain.SourceVerified, time.Now()))
	require.Equal(t, domain.SourceDisabled, statusOf(t, g, "stopped"))

	require.NoError(t, repo.MarkBlocked(ctx, "stopped"))
	require.Equal(t, domain.SourceDisabled, statusOf(t, g, "stopped"))

	// Seed reload declaring the source paused, and source discovery
	// re-finding its domain (status pending): neither may un-stop it.
	for _, st := range []domain.SourceStatus{domain.SourcePaused, domain.SourcePending} {
		require.NoError(t, repo.Upsert(ctx, &domain.Source{
			Type: domain.SourceGenericHTML, BaseURL: "https://stopped.example.com", Status: st, Name: "renamed",
			Kinds: pq.StringArray{"job"}, RequiredAttributesByKind: map[string][]string{},
			Language: "en", Priority: 1, CrawlIntervalSec: 3600,
		}))
		require.Equal(t, domain.SourceDisabled, statusOf(t, g, "stopped"), "upsert with %s", st)
	}

	// System auto-approve after verification (and operator approve) only
	// promote a verified source.
	require.NoError(t, repo.Approve(ctx, "stopped", "system", time.Now()))
	require.Equal(t, domain.SourceDisabled, statusOf(t, g, "stopped"))

	// Operator actions still work.
	require.NoError(t, repo.EnableSource(ctx, "stopped"))
	require.Equal(t, domain.SourceActive, statusOf(t, g, "stopped"))
}

// The guards must not break the normal health lifecycle.
func TestSourceStatus_HealthLifecycleForCrawlingSources(t *testing.T) {
	g, _, ctx := setupCandidateDB(t)
	repo := repository.NewSourceRepository(func(context.Context, bool) *gorm.DB { return g })
	newSource(t, g, "s", domain.SourceActive)
	newSource(t, g, "pending", domain.SourcePending)

	require.NoError(t, repo.RecordFailure(ctx, "s", 0.4, 2))
	require.Equal(t, domain.SourceActive, statusOf(t, g, "s"))
	require.NoError(t, repo.RecordFailure(ctx, "s", 0.3, 3))
	require.Equal(t, domain.SourceDegraded, statusOf(t, g, "s"))
	require.NoError(t, repo.RecordFailure(ctx, "s", 0.1, 5))
	require.Equal(t, domain.SourcePaused, statusOf(t, g, "s"))

	// Failures of a not-yet-crawling source do not invent a status.
	require.NoError(t, repo.RecordFailure(ctx, "pending", 0.1, 5))
	require.Equal(t, domain.SourcePending, statusOf(t, g, "pending"))

	require.NoError(t, repo.EnableSource(ctx, "s"))
	changed, err := repo.TransitionStatus(ctx, "s", []domain.SourceStatus{domain.SourceActive}, domain.SourceDegraded)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repo.RecordSuccess(ctx, "s", 1.0))
	require.Equal(t, domain.SourceActive, statusOf(t, g, "s"), "degraded recovers on success")

	require.NoError(t, repo.SaveVerificationReport(ctx, "pending", &domain.VerificationReport{URLValid: true}, domain.SourceVerified, time.Now()))
	require.Equal(t, domain.SourceVerified, statusOf(t, g, "pending"))
	require.NoError(t, repo.Approve(ctx, "pending", "op", time.Now()))
	require.Equal(t, domain.SourceActive, statusOf(t, g, "pending"), "verified → active still works")
}
