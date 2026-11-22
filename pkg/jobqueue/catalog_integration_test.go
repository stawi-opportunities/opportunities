//go:build integration

package jobqueue_test

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

// dualDB is the production topology: crawl plane and product plane in two
// separate databases, each with only its own tables (real GORM models).
type dualDB struct {
	ctx            context.Context
	crawl, product *gorm.DB
	store          *jobqueue.Store
}

func newDualDB(t *testing.T) *dualDB {
	t.Helper()
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
	return &dualDB{ctx: ctx, crawl: crawl, product: product, store: store}
}

func (d *dualDB) addSource(t *testing.T, id string, status domain.SourceStatus) {
	t.Helper()
	require.NoError(t, d.crawl.Create(&domain.Source{
		BaseModel: domain.BaseModel{ID: id}, Type: domain.SourceGenericHTML,
		BaseURL: "https://" + id + ".example.com", Status: status,
	}).Error)
}

func (d *dualDB) setStatus(t *testing.T, id string, status domain.SourceStatus) {
	t.Helper()
	require.NoError(t, d.crawl.Exec(`UPDATE sources SET status=? WHERE id=?`, string(status), id).Error)
}

// enqueueAndClaim puts one item for source/hardKey on the crawl queue and
// claims it, returning the leased item.
func (d *dualDB) enqueueAndClaim(t *testing.T, source, hardKey, external string) jobqueue.Item {
	t.Helper()
	body, err := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
		VariantID: "v-" + external, SourceID: source, ExternalID: external, HardKey: hardKey,
		Kind: "job", Title: "Engineer", ApplyURL: "https://x.test/" + external, ScrapedAt: time.Now().UTC(),
	}))
	require.NoError(t, err)
	require.NoError(t, d.store.Enqueue(d.ctx, jobqueue.EnqueueRequest{
		VariantID: "v-" + external, SourceID: source, IdempotencyKey: "k-" + external, Payload: body,
	}))
	items, err := d.store.Claim(d.ctx, "worker-"+external, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, items, 1)
	return items[0]
}

func (d *dualDB) complete(t *testing.T, item jobqueue.Item, canonical, hardKey string) {
	t.Helper()
	_, err := d.store.Complete(d.ctx, item, jobqueue.Canonical{
		CandidateID: canonical, HardKey: hardKey, Kind: "job", SourceID: item.SourceID,
		ExternalID: item.VariantID, Title: "Engineer", ApplyURL: "https://x.test/" + item.VariantID,
		SeenAt: time.Now().UTC(), Attributes: map[string]any{},
	})
	require.NoError(t, err)
}

func (d *dualDB) hidden(t *testing.T, canonical string) (bool, string) {
	t.Helper()
	var row struct {
		Hidden       bool
		HiddenReason *string
	}
	require.NoError(t, d.product.Raw(`SELECT hidden, hidden_reason FROM opportunities WHERE canonical_id=?`, canonical).Scan(&row).Error)
	reason := ""
	if row.HiddenReason != nil {
		reason = *row.HiddenReason
	}
	return row.Hidden, reason
}

func (d *dualDB) commandStatus(t *testing.T) string {
	t.Helper()
	var status string
	// The original command, not the follow-up sweep an applied one schedules.
	require.NoError(t, d.crawl.Raw(`SELECT status FROM catalog_commands WHERE NOT followup ORDER BY created_at DESC LIMIT 1`).Scan(&status).Error)
	return status
}

func TestStopSource_HidesOnlyItsListings_OnProductDB(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceActive)
	d.addSource(t, "s2", domain.SourceActive)

	// only-s1: listed by s1 only. shared: listed by s1 and s2.
	d.complete(t, d.enqueueAndClaim(t, "s1", "only-s1", "a"), "c-only", "only-s1")
	d.complete(t, d.enqueueAndClaim(t, "s1", "shared", "b"), "c-shared", "shared")
	d.complete(t, d.enqueueAndClaim(t, "s2", "shared", "c"), "c-shared", "shared")

	// Operator stop, as the crawler does it.
	d.setStatus(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "source stopped: spam"))

	// Nothing touches the catalog until the worker applies the command.
	h, _ := d.hidden(t, "c-only")
	require.False(t, h)

	cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w1", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, cmd)
	out, err := d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandApplied, out)

	h, reason := d.hidden(t, "c-only")
	require.True(t, h)
	require.Equal(t, "source stopped: spam", reason)
	h, _ = d.hidden(t, "c-shared")
	require.False(t, h, "a listing another active source still carries stays visible")
	require.Equal(t, "done", d.commandStatus(t))

	// The crawl DB never received catalog tables.
	require.False(t, d.crawl.Migrator().HasTable("opportunities"))

	next, err := d.store.ClaimCatalogCommand(d.ctx, "w1", time.Minute)
	require.NoError(t, err)
	require.Nil(t, next)
}

// An item claimed before the stop must not re-show listings after the
// deactivation has run.
func TestStopSource_DefersUntilInFlightIngestDrains(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceActive)
	d.complete(t, d.enqueueAndClaim(t, "s1", "job-1", "a"), "c1", "job-1")

	inFlight := d.enqueueAndClaim(t, "s1", "job-1", "b") // claimed, not yet committed

	d.setStatus(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "source stopped"))

	cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w1", time.Minute)
	require.NoError(t, err)
	out, err := d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandDeferred, out)
	require.Equal(t, "pending", d.commandStatus(t))
	var attempt int
	var deferred bool
	require.NoError(t, d.crawl.Raw(`SELECT attempt, available_at > now() FROM catalog_commands`).Row().Scan(&attempt, &deferred))
	require.Equal(t, 0, attempt, "deferral must not burn retry attempts")
	require.True(t, deferred)
	again, err := d.store.ClaimCatalogCommand(d.ctx, "w1", time.Minute)
	require.NoError(t, err)
	require.Nil(t, again, "deferred command is not claimable before its delay")

	// The in-flight item commits (it passed the stopped check before the stop).
	d.complete(t, inFlight, "c1", "job-1")
	h, _ := d.hidden(t, "c1")
	require.False(t, h)

	require.NoError(t, d.crawl.Exec(`UPDATE catalog_commands SET available_at=now()`).Error)
	cmd, err = d.store.ClaimCatalogCommand(d.ctx, "w1", time.Minute)
	require.NoError(t, err)
	out, err = d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandApplied, out)
	h, _ = d.hidden(t, "c1")
	require.True(t, h)
}

func TestStopSource_SupersededWhenRestartedBeforeApply(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceActive)
	d.complete(t, d.enqueueAndClaim(t, "s1", "job-1", "a"), "c1", "job-1")

	d.setStatus(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "oops"))
	d.setStatus(t, "s1", domain.SourceActive) // operator restarts quickly

	cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w1", time.Minute)
	require.NoError(t, err)
	out, err := d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandSuperseded, out)
	h, _ := d.hidden(t, "c1")
	require.False(t, h)
	require.Equal(t, "superseded", d.commandStatus(t))
}

func TestCatalogCommand_StaleOwnerIsFenced(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "x"))

	stale, err := d.store.ClaimCatalogCommand(d.ctx, "w-old", time.Millisecond)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	fresh, err := d.store.ClaimCatalogCommand(d.ctx, "w-new", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, fresh, "expired lease is reclaimed")
	require.Equal(t, stale.ID, fresh.ID)

	out, err := d.store.RunCatalogCommand(d.ctx, *stale)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandLostLease, out)
	require.NoError(t, d.store.FailCatalogCommand(d.ctx, *stale, assertErr("stale"), 3))
	require.Equal(t, "processing", d.commandStatus(t), "stale worker cannot overwrite the new owner's claim")

	out, err = d.store.RunCatalogCommand(d.ctx, *fresh)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandApplied, out)
	var followups int64
	require.NoError(t, d.crawl.Raw(`SELECT count(*) FROM catalog_commands WHERE followup`).Scan(&followups).Error)
	require.EqualValues(t, 1, followups, "the stale owner must not schedule a follow-up")
}

func TestCatalogCommand_FailBacksOffThenDies(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "x"))

	for i := 1; i <= 2; i++ {
		require.NoError(t, d.crawl.Exec(`UPDATE catalog_commands SET available_at=now()`).Error)
		cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w", time.Minute)
		require.NoError(t, err)
		require.Equal(t, i, cmd.Attempt)
		require.NoError(t, d.store.FailCatalogCommand(d.ctx, *cmd, assertErr("product db down"), 2))
	}
	require.Equal(t, "dead", d.commandStatus(t))
	var lastErr string
	require.NoError(t, d.crawl.Raw(`SELECT last_error FROM catalog_commands`).Scan(&lastErr).Error)
	require.Equal(t, "product db down", lastErr)
}

func TestDiscard_DropsItemOfStoppedSourceWithAudit(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceActive)
	item := d.enqueueAndClaim(t, "s1", "job-1", "a")

	stopped, err := d.store.SourceStopped(d.ctx, "s1")
	require.NoError(t, err)
	require.False(t, stopped)
	d.setStatus(t, "s1", domain.SourceDisabled)
	stopped, err = d.store.SourceStopped(d.ctx, "s1")
	require.NoError(t, err)
	require.True(t, stopped)
	unknown, err := d.store.SourceStopped(d.ctx, "nope")
	require.NoError(t, err)
	require.False(t, unknown)

	require.NoError(t, d.store.Discard(d.ctx, item, "source stopped"))
	var status string
	require.NoError(t, d.crawl.Raw(`SELECT status FROM job_ingest_queue WHERE id=?`, item.ID).Scan(&status).Error)
	require.Equal(t, "rejected", status)
	var events int64
	require.NoError(t, d.crawl.Raw(`SELECT count(*) FROM job_ingest_events WHERE ingest_id=? AND event_type='discarded'`, item.ID).Scan(&events).Error)
	require.EqualValues(t, 1, events)
	require.Error(t, d.store.Discard(d.ctx, item, "again"), "a non-processing item cannot be discarded twice")
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func TestCatalogCommand_AppliedDeactivationSchedulesOneFollowup(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "stop"))

	cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w", time.Minute)
	require.NoError(t, err)
	require.False(t, cmd.Followup)
	out, err := d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandApplied, out)

	var followups int64
	var delayed bool
	require.NoError(t, d.crawl.Raw(`SELECT count(*), bool_and(available_at > now() + interval '5 minutes')
		FROM catalog_commands WHERE followup AND status='pending'`).Row().Scan(&followups, &delayed))
	require.EqualValues(t, 1, followups)
	require.True(t, delayed, "follow-up waits past the ingest lease")

	require.NoError(t, d.crawl.Exec(`UPDATE catalog_commands SET available_at=now() WHERE followup`).Error)
	fu, err := d.store.ClaimCatalogCommand(d.ctx, "w", time.Minute)
	require.NoError(t, err)
	require.True(t, fu.Followup)
	out, err = d.store.RunCatalogCommand(d.ctx, *fu)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandApplied, out)
	var pending int64
	require.NoError(t, d.crawl.Raw(`SELECT count(*) FROM catalog_commands WHERE status='pending'`).Scan(&pending).Error)
	require.Zero(t, pending, "a follow-up never schedules another")
}

func TestCatalogCommand_BacklogCountsDeadAndStale(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceDisabled)
	dead, stale, err := d.store.CommandBacklog(d.ctx, 30*time.Minute)
	require.NoError(t, err)
	require.Zero(t, dead+stale)

	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "a"))
	require.NoError(t, d.crawl.Exec(`UPDATE catalog_commands SET status='dead' WHERE reason='a'`).Error)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "b"))
	require.NoError(t, d.crawl.Exec(`UPDATE catalog_commands SET created_at=now()-interval '1 hour' WHERE reason='b'`).Error)
	dead, stale, err = d.store.CommandBacklog(d.ctx, 30*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, dead)
	require.EqualValues(t, 1, stale)
}

// Items claimed after the stop are discarded by the worker and can never
// write the catalog, so they must not hold the deactivation back.
func TestStopSource_ItemsClaimedAfterStopDoNotDefer(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceActive)
	d.complete(t, d.enqueueAndClaim(t, "s1", "job-1", "a"), "c1", "job-1")

	d.setStatus(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "stop"))
	time.Sleep(5 * time.Millisecond)
	_ = d.enqueueAndClaim(t, "s1", "job-2", "b") // backlog item claimed after the stop

	cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w", time.Minute)
	require.NoError(t, err)
	out, err := d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	require.Equal(t, jobqueue.CommandApplied, out)
	h, _ := d.hidden(t, "c1")
	require.True(t, h)
}

func TestRequestSourceDeactivation_OnePendingPerSource(t *testing.T) {
	d := newDualDB(t)
	d.addSource(t, "s1", domain.SourceDisabled)
	d.addSource(t, "s2", domain.SourceDisabled)
	for i := 0; i < 5; i++ {
		require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "late write"))
	}
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s2", "stop"))
	var s1, s2 int64
	require.NoError(t, d.crawl.Raw(`SELECT count(*) FROM catalog_commands WHERE source_id='s1'`).Scan(&s1).Error)
	require.NoError(t, d.crawl.Raw(`SELECT count(*) FROM catalog_commands WHERE source_id='s2'`).Scan(&s2).Error)
	require.EqualValues(t, 1, s1)
	require.EqualValues(t, 1, s2)

	// Once the pending one has run, a new request is accepted again.
	cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w", time.Minute)
	require.NoError(t, err)
	_, err = d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, cmd.SourceID, "again"))
	var pending int64
	require.NoError(t, d.crawl.Raw(`SELECT count(*) FROM catalog_commands WHERE source_id=? AND status='pending' AND NOT followup`, cmd.SourceID).Scan(&pending).Error)
	require.EqualValues(t, 1, pending)
}

func TestFollowupDelayCoversLongLease(t *testing.T) {
	d := newDualDB(t)
	d.store.WithIngestLease(20 * time.Minute)
	d.addSource(t, "s1", domain.SourceDisabled)
	require.NoError(t, d.store.RequestSourceDeactivation(d.ctx, "s1", "stop"))
	cmd, err := d.store.ClaimCatalogCommand(d.ctx, "w", time.Minute)
	require.NoError(t, err)
	_, err = d.store.RunCatalogCommand(d.ctx, *cmd)
	require.NoError(t, err)
	var ok bool
	require.NoError(t, d.crawl.Raw(`SELECT available_at > now() + interval '55 minutes' FROM catalog_commands WHERE followup`).Scan(&ok).Error)
	require.True(t, ok, "follow-up is 3x the lease when that exceeds the 10m floor")
}
