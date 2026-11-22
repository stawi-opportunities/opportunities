//go:build integration

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// The worker must not merge queued work of an operator-stopped source into
// the product catalog, while other sources drain normally.
func TestProcessor_DiscardsStoppedSourceAndAppliesStopCommand(t *testing.T) {
	ctx := context.Background()
	open := func() *gorm.DB {
		g, err := gorm.Open(postgres.New(postgres.Config{Conn: testhelpers.PostgresContainerNoMigrate(t, ctx)}), &gorm.Config{})
		require.NoError(t, err)
		return g
	}
	crawl, product := open(), open()
	require.NoError(t, crawl.AutoMigrate(&domain.Source{}, &jobqueue.QueueRecord{}, &jobqueue.IngestEventRecord{}, &jobqueue.CatalogCommandRecord{}))
	require.NoError(t, product.AutoMigrate(&jobqueue.OpportunityRecord{}, &jobqueue.OpportunityIdentityRecord{}, &jobqueue.OpportunitySourceRecord{}))
	store := jobqueue.New(func(context.Context, bool) *gorm.DB { return crawl }).
		WithProductDB(func(context.Context, bool) *gorm.DB { return product })

	for _, id := range []string{"live", "stopped"} {
		require.NoError(t, crawl.Create(&domain.Source{
			BaseModel: domain.BaseModel{ID: id}, Type: domain.SourceGenericHTML,
			BaseURL: "https://" + id + ".example.com", Status: domain.SourceActive,
		}).Error)
	}
	enqueue := func(source, hardKey string) {
		body, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
			VariantID: "v-" + hardKey, SourceID: source, ExternalID: hardKey, HardKey: hardKey,
			Kind: "job", Title: "Engineer " + hardKey, ApplyURL: "https://x.test/" + hardKey, ScrapedAt: time.Now().UTC(),
		}))
		require.NoError(t, err)
		require.NoError(t, store.Enqueue(ctx, jobqueue.EnqueueRequest{
			VariantID: "v-" + hardKey, SourceID: source, IdempotencyKey: "k-" + hardKey, Payload: body,
		}))
	}

	proc := NewPostgresProcessor(store, "w", 10, 1, time.Second, time.Minute, 3)
	enqueue("stopped", "old-job")
	require.NoError(t, proc.drain(ctx))

	// Stop the source (as the crawler does), then more of its work arrives.
	require.NoError(t, crawl.Exec(`UPDATE sources SET status='disabled' WHERE id='stopped'`).Error)
	require.NoError(t, store.RequestSourceDeactivation(ctx, "stopped", "source stopped: test"))
	enqueue("stopped", "late-job")
	enqueue("live", "live-job")
	require.NoError(t, proc.drain(ctx))

	var titles []string
	require.NoError(t, product.Raw(`SELECT title FROM opportunities WHERE hidden=false ORDER BY title`).Scan(&titles).Error)
	require.Equal(t, []string{"Engineer live-job", "Engineer old-job"}, titles, "late work of the stopped source is not merged")
	var statuses []string
	require.NoError(t, crawl.Raw(`SELECT status FROM job_ingest_queue WHERE source_id='stopped' ORDER BY created_at`).Scan(&statuses).Error)
	require.Equal(t, []string{"processed", "rejected"}, statuses)

	// The command runner hides what the source had already published.
	NewCatalogCommandRunner(store, time.Second).drain(ctx)
	require.NoError(t, product.Raw(`SELECT title FROM opportunities WHERE hidden=false ORDER BY title`).Scan(&titles).Error)
	require.Equal(t, []string{"Engineer live-job"}, titles)
}

// Reviewer scenario: a worker passes the stopped-source check, stalls past
// its lease while the stop's deactivation runs, then commits to the product
// DB. The listing must end up hidden again, and the stale worker must not be
// able to resolve the queue row another worker already discarded.
func TestProcessor_StaleLeaseWriterCannotResurrectStoppedSource(t *testing.T) {
	ctx := context.Background()
	open := func() *gorm.DB {
		g, err := gorm.Open(postgres.New(postgres.Config{Conn: testhelpers.PostgresContainerNoMigrate(t, ctx)}), &gorm.Config{})
		require.NoError(t, err)
		return g
	}
	crawl, product := open(), open()
	require.NoError(t, crawl.AutoMigrate(&domain.Source{}, &jobqueue.QueueRecord{}, &jobqueue.IngestEventRecord{}, &jobqueue.CatalogCommandRecord{}))
	require.NoError(t, product.AutoMigrate(&jobqueue.OpportunityRecord{}, &jobqueue.OpportunityIdentityRecord{}, &jobqueue.OpportunitySourceRecord{}))
	store := jobqueue.New(func(context.Context, bool) *gorm.DB { return crawl }).
		WithProductDB(func(context.Context, bool) *gorm.DB { return product })
	require.NoError(t, crawl.Create(&domain.Source{
		BaseModel: domain.BaseModel{ID: "s1"}, Type: domain.SourceGenericHTML,
		BaseURL: "https://s1.example.com", Status: domain.SourceActive,
	}).Error)
	body, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
		VariantID: "v1", SourceID: "s1", ExternalID: "e1", HardKey: "job-1",
		Kind: "job", Title: "Engineer", ApplyURL: "https://x.test/e1", ScrapedAt: time.Now().UTC(),
	}))
	require.NoError(t, err)
	require.NoError(t, store.Enqueue(ctx, jobqueue.EnqueueRequest{VariantID: "v1", SourceID: "s1", IdempotencyKey: "k1", Payload: body}))

	// W1 claims (short lease) and passes the stopped check: source active.
	w1Items, err := store.Claim(ctx, "w1", 1, time.Millisecond)
	require.NoError(t, err)
	require.Len(t, w1Items, 1)
	stale := w1Items[0]
	stopped, err := store.SourceStopped(ctx, "s1")
	require.NoError(t, err)
	require.False(t, stopped)

	// Operator stops the source; W1 stalls past its lease.
	require.NoError(t, crawl.Exec(`UPDATE sources SET status='disabled' WHERE id='s1'`).Error)
	require.NoError(t, store.RequestSourceDeactivation(ctx, "s1", "source stopped"))
	time.Sleep(20 * time.Millisecond)

	// W2 reclaims the expired item and discards it; the command applies.
	w2Items, err := store.Claim(ctx, "w2", 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, w2Items, 1)
	require.NoError(t, store.Discard(ctx, w2Items[0], "source stopped"))
	runner := NewCatalogCommandRunner(store, time.Second)
	runner.drain(ctx)

	// W1 wakes up and commits its catalog write; its ack is fenced out.
	_, completeErr := store.Complete(ctx, stale, jobqueue.Canonical{
		CandidateID: "c1", HardKey: "job-1", Kind: "job", SourceID: "s1", ExternalID: "e1",
		Title: "Engineer", ApplyURL: "https://x.test/e1", SeenAt: time.Now().UTC(), Attributes: map[string]any{},
	})
	require.Error(t, completeErr, "stale ack must not resolve a row it no longer owns")
	require.ErrorIs(t, store.Retry(ctx, stale, completeErr, 3), jobqueue.ErrLostLease)
	var status string
	require.NoError(t, crawl.Raw(`SELECT status FROM job_ingest_queue WHERE id=?`, stale.ID).Scan(&status).Error)
	require.Equal(t, "rejected", status, "W2's discard stands")

	// The stale write re-showed the listing; the worker's post-write check
	// re-queues the deactivation, which hides it again.
	var hidden bool
	require.NoError(t, product.Raw(`SELECT hidden FROM opportunities WHERE canonical_id='c1'`).Scan(&hidden).Error)
	require.False(t, hidden, "precondition: the stale write did resurrect the listing")
	proc := NewPostgresProcessor(store, "w1", 1, 1, time.Second, time.Minute, 3)
	proc.reapplyStopIfNeeded(ctx, stale)
	runner.drain(ctx)
	require.NoError(t, product.Raw(`SELECT hidden FROM opportunities WHERE canonical_id='c1'`).Scan(&hidden).Error)
	require.True(t, hidden)
}
