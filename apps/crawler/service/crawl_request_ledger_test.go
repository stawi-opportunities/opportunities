//go:build integration

package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frametest"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// multiPageIterator is a CrawlIterator that yields a fixed sequence of
// pages, each with its own RawHTML + Items batch. Lets the ledger-trail
// test prove that one crawl produces exactly N raw_payloads rows for N
// pages.
type multiPageIterator struct {
	pages []ledgerPage
	idx   int
	cur   ledgerPage
}

type ledgerPage struct {
	rawHTML []byte
	items   []domain.ExternalOpportunity
}

func (m *multiPageIterator) Next(_ context.Context) bool {
	if m.idx >= len(m.pages) {
		return false
	}
	m.cur = m.pages[m.idx]
	m.idx++
	return true
}

func (m *multiPageIterator) Items() []domain.ExternalOpportunity { return m.cur.items }
func (m *multiPageIterator) RawPayload() []byte                  { return m.cur.rawHTML }
func (m *multiPageIterator) HTTPStatus() int                     { return 200 }
func (m *multiPageIterator) Err() error                          { return nil }
func (m *multiPageIterator) Cursor() json.RawMessage             { return nil }
func (m *multiPageIterator) Content() *content.Extracted {
	if len(m.cur.rawHTML) == 0 {
		return nil
	}
	return &content.Extracted{RawHTML: string(m.cur.rawHTML)}
}

// multiPageConnector yields a fixed multi-page sequence.
type multiPageConnector struct {
	pages []ledgerPage
}

func (c *multiPageConnector) Type() domain.SourceType { return domain.SourceGenericHTML }
func (c *multiPageConnector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return &multiPageIterator{pages: c.pages}
}

// makeJobItems returns n minimally-valid job opportunities that pass
// opportunity.Verify. Each item is keyed by an externalID prefix so
// (prefix, n) generates unique hard keys across pages.
func makeJobItems(prefix string, n int) []domain.ExternalOpportunity {
	out := make([]domain.ExternalOpportunity, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.ExternalOpportunity{
			Kind:          "job",
			ExternalID:    prefix + "-" + itoa(i),
			Title:         "Backend Engineer " + prefix + "-" + itoa(i),
			IssuingEntity: "Acme",
			ApplyURL:      "https://acme.example/jobs/" + prefix + "-" + itoa(i),
			Description:   "We are looking for a skilled backend engineer to join our team and build scalable services.",
		})
	}
	return out
}

// itoa is a tiny strconv-free int→string for test IDs. Keeps the test
// import set minimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// envSilentCollector drops envelopes; tests that only care about DB
// side-effects can use this as a "subscriber exists" stub so Frame's
// publisher initialisation has a counterpart consumer.
type envSilentCollector[P any] struct {
	topic string
	mu    sync.Mutex
	n     int
}

func (c *envSilentCollector[P]) Name() string                            { return c.topic }
func (c *envSilentCollector[P]) PayloadType() any                        { var raw json.RawMessage; return &raw }
func (c *envSilentCollector[P]) Validate(context.Context, any) error     { return nil }
func (c *envSilentCollector[P]) Execute(_ context.Context, _ any) error  { c.mu.Lock(); c.n++; c.mu.Unlock(); return nil }

// TestExecute_FullLedgerTrail proves that one crawl.request produces:
//   - exactly 1 crawl_jobs row with status='succeeded' and jobs_found = N items;
//   - exactly 1 raw_payloads row per iterator page, all status='pending';
//   - N pipeline_variants rows, each linked to both ledger tables.
//
// Runs under //go:build integration because it relies on a real Postgres
// (testcontainers) — the in-memory test scaffold can't exercise the
// hypertable schema or the variant-store gorm writes.
func TestExecute_FullLedgerTrail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// ── Postgres: stub + migrations + AutoMigrate pipeline_variants ────
	sqlDB := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, sqlDB))
	testhelpers.ApplyMigrationsDir(t, ctx, sqlDB, "../../../db/migrations")

	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	// Materialise the full pipeline_variants column set. The stub created
	// the table with only (variant_id, ingested_at); migration 0019 added
	// the two ledger forward-links. AutoMigrate fills in everything the
	// Variant struct expects (source_id, hard_key, kind, current_stage,
	// stage_at, attempts, created_at, updated_at) without disturbing the
	// hypertable's composite PK.
	require.NoError(t, g.AutoMigrate(&variantstate.Variant{}))

	dbFn := func(_ context.Context, _ bool) *gorm.DB { return g }
	crawlRepo := repository.NewCrawlRepository(dbFn)
	variantStore := variantstate.NewStore(dbFn)

	// ── Frame service with noop driver ─────────────────────────────────
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-ledger-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// Silent collectors so Frame initialises publishers for these
	// topics — the handler emits to them and we don't assert on the
	// payloads (the DB ledger is the assertion surface).
	svc.EventsManager().Add(&envSilentCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested})
	svc.EventsManager().Add(&envSilentCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted})

	go func() { _ = svc.Run(ctx, "") }()
	frametest.WaitPublisherReady(t, svc, eventsv1.TopicVariantsIngested, 2*time.Second)
	frametest.WaitPublisherReady(t, svc, eventsv1.TopicCrawlPageCompleted, 2*time.Second)

	// ── Connector: two pages with 3 + 2 items → 5 variants total ───────
	connReg := connectors.NewRegistry()
	connReg.Register(&multiPageConnector{
		pages: []ledgerPage{
			{rawHTML: []byte("<html>page1</html>"), items: makeJobItems("p1", 3)},
			{rawHTML: []byte("<html>page2</html>"), items: makeJobItems("p2", 2)},
		},
	})

	srcs := &fakeSourceGetter{
		rows: map[string]*domain.Source{
			"s-full": {
				BaseModel: domain.BaseModel{ID: "s-full"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://acme.example/jobs",
				Status:    domain.SourceActive,
				Country:   "KE",
				Language:  "en",
			},
		},
	}

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:            svc,
		Sources:        srcs,
		Registry:       connReg,
		Archive:        archive.NewFakeArchive(),
		Extractor:      nil,
		DiscoverSample: 0,
		VariantStore:   variantStore,
		CrawlRepo:      crawlRepo,
	})

	idemKey := "src-full:2026-05-28T00:00:00Z"
	scheduledAt, _ := time.Parse(time.RFC3339, "2026-05-28T00:00:00Z")
	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID:      "req-full",
		SourceID:       "s-full",
		Mode:           "auto",
		Attempt:        1,
		IdempotencyKey: idemKey,
		ScheduledAt:    scheduledAt,
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	require.NoError(t, h.Execute(ctx, &rm))

	// ── Assertions ─────────────────────────────────────────────────────

	// One crawl_jobs row, succeeded, jobs_found=5.
	type jobRow struct {
		ID        string
		Status    string
		JobsFound int
	}
	var got jobRow
	require.NoError(t, g.Raw(
		`SELECT id, status, jobs_found FROM crawl_jobs WHERE idempotency_key = ?`,
		idemKey,
	).Scan(&got).Error)
	require.Equal(t, "succeeded", got.Status, "crawl_jobs.status")
	require.Equal(t, 5, got.JobsFound, "crawl_jobs.jobs_found")
	require.NotEmpty(t, got.ID, "crawl_jobs.id should be populated")

	// Two raw_payloads rows, all pending, all linked to this crawl_job.
	var rawCount int64
	require.NoError(t, g.Raw(
		`SELECT count(*) FROM raw_payloads WHERE crawl_job_id = ? AND status = 'pending'`,
		got.ID,
	).Scan(&rawCount).Error)
	require.Equal(t, int64(2), rawCount, "raw_payloads count")

	// Five pipeline_variants linked to both ledger tables.
	var variantCount int64
	require.NoError(t, g.Raw(
		`SELECT count(*) FROM pipeline_variants
            WHERE crawl_job_id = ? AND raw_payload_id IS NOT NULL`,
		got.ID,
	).Scan(&variantCount).Error)
	require.Equal(t, int64(5), variantCount, "pipeline_variants linked to ledger")
}
