//go:build integration

package integration

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

type MatchingPipelineSuite struct {
	suite.Suite
	db  *sql.DB
	ctx context.Context

	store    *matching.Store
	events   *matching.EventLog
	idx      *matching.IndexStore
	knn      *matching.KNN
	reranker matching.Reranker
}

func TestMatchingPipelineSuite(t *testing.T) { suite.Run(t, new(MatchingPipelineSuite)) }

func (s *MatchingPipelineSuite) SetupSuite() {
	s.ctx = context.Background()
	s.db = testhelpers.PostgresContainerNoMigrate(s.T(), s.ctx)
	require.NoError(s.T(), testhelpers.EnsureOpportunitiesStub(s.ctx, s.db))

	// Enable pgvector before using the vector type in the ALTER TABLE below.
	_, err := s.db.ExecContext(s.ctx, `CREATE EXTENSION IF NOT EXISTS vector`)
	require.NoError(s.T(), err)

	// Opportunities need extra columns for KNN; the minimal stub doesn't include them.
	_, err = s.db.ExecContext(s.ctx, `
		ALTER TABLE opportunities
			ADD COLUMN IF NOT EXISTS embedding vector(1536),
			ADD COLUMN IF NOT EXISTS kind TEXT,
			ADD COLUMN IF NOT EXISTS country TEXT,
			ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ DEFAULT now()
	`)
	require.NoError(s.T(), err)
	testhelpers.ApplyMigrationsDir(s.T(), s.ctx, s.db, "../../db/migrations")

	s.store = matching.NewStore(s.db)
	s.events = matching.NewEventLog(s.db)
	s.idx = matching.NewIndexStore(s.db)
	s.knn = matching.NewKNN(s.db)
	s.reranker = matching.NoopReranker{}
}

// TestPathA_FanOutHappyPath verifies that a single FanOut call writes one
// candidate_match row and one candidate_match_event row.
// Isolation: kind="grant_a", country="TZ" — no other test uses this pair.
func (s *MatchingPipelineSuite) TestPathA_FanOutHappyPath() {
	s.seedCandidate("c1", mpMakeUnitVector(1536, 0), 0.5, []string{"grant_a"}, []string{"TZ"})
	s.seedOpportunity("o1", mpMakeUnitVector(1536, 0), "grant_a", "TZ")

	res, err := matching.FanOut(s.ctx, matching.FanOutInput{
		CanonicalID:   "can_1",
		OpportunityID: "o1",
		Kind:          "grant_a",
		Country:       "TZ",
		Embedding:     mpMakeUnitVector(1536, 0),
		FirstSeenAt:   time.Now(),
	}, matching.FanOutDeps{
		KNN: s.knn, Store: s.store, EventLog: s.events,
		Reranker: s.reranker, Weights: matching.DefaultWeights(),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, res.MatchesWritten)

	got, err := s.store.GetByPair(s.ctx, "c1", "o1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), matching.StatusNew, got.Status)

	var eventCount int
	require.NoError(s.T(), s.db.QueryRowContext(s.ctx,
		`SELECT count(*) FROM candidate_match_events WHERE candidate_id='c1' AND opportunity_id='o1'`).
		Scan(&eventCount))
	require.Equal(s.T(), 1, eventCount)
}

// TestPathA_IdempotentReplay verifies that running FanOut 3× on the same input
// produces exactly 1 row (ON CONFLICT idempotency).
// Isolation: kind="grant_b", country="TZ".
func (s *MatchingPipelineSuite) TestPathA_IdempotentReplay() {
	s.seedCandidate("c2", mpMakeUnitVector(1536, 1), 0.5, []string{"grant_b"}, []string{"TZ"})
	s.seedOpportunity("o2", mpMakeUnitVector(1536, 1), "grant_b", "TZ")
	in := matching.FanOutInput{
		CanonicalID: "can_2", OpportunityID: "o2", Kind: "grant_b", Country: "TZ",
		Embedding: mpMakeUnitVector(1536, 1), FirstSeenAt: time.Now(),
	}
	deps := matching.FanOutDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
		Reranker: s.reranker, Weights: matching.DefaultWeights()}

	for i := 0; i < 3; i++ {
		_, err := matching.FanOut(s.ctx, in, deps)
		require.NoError(s.T(), err)
	}
	var rowCnt int
	require.NoError(s.T(), s.db.QueryRowContext(s.ctx,
		`SELECT count(*) FROM candidate_matches WHERE candidate_id='c2' AND opportunity_id='o2'`).
		Scan(&rowCnt))
	require.Equal(s.T(), 1, rowCnt, "3 replays must collapse into 1 row")
}

// TestTerminalStateImmuneToFanOut verifies that a dismissed match is not
// resurrected by a subsequent FanOut replay.
// Isolation: kind="grant_c", country="TZ".
func (s *MatchingPipelineSuite) TestTerminalStateImmuneToFanOut() {
	s.seedCandidate("c3", mpMakeUnitVector(1536, 2), 0.5, []string{"grant_c"}, []string{"TZ"})
	s.seedOpportunity("o3", mpMakeUnitVector(1536, 2), "grant_c", "TZ")

	in := matching.FanOutInput{CanonicalID: "can_3", OpportunityID: "o3", Kind: "grant_c",
		Country: "TZ", Embedding: mpMakeUnitVector(1536, 2), FirstSeenAt: time.Now()}
	deps := matching.FanOutDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
		Reranker: s.reranker, Weights: matching.DefaultWeights()}
	_, err := matching.FanOut(s.ctx, in, deps)
	require.NoError(s.T(), err)

	// Mark dismissed (simulates user action).
	_, err = s.db.ExecContext(s.ctx,
		`UPDATE candidate_matches SET status='dismissed', dismissed_at=now()
		 WHERE candidate_id='c3' AND opportunity_id='o3'`)
	require.NoError(s.T(), err)

	// Replay must not resurrect.
	_, err = matching.FanOut(s.ctx, in, deps)
	require.NoError(s.T(), err)

	got, err := s.store.GetByPair(s.ctx, "c3", "o3")
	require.NoError(s.T(), err)
	require.Equal(s.T(), matching.StatusDismissed, got.Status)
}

// TestPathB_MergesWithoutDuplicates verifies that GapFill on a candidate that
// already has a FanOut match does not create a duplicate row.
// Isolation: kind="grant_d", country="TZ" — only o4 satisfies this filter.
func (s *MatchingPipelineSuite) TestPathB_MergesWithoutDuplicates() {
	s.seedCandidate("c4", mpMakeUnitVector(1536, 3), 0.4, []string{"grant_d"}, []string{"TZ"})
	s.seedOpportunity("o4", mpMakeUnitVector(1536, 3), "grant_d", "TZ")

	fanout := matching.FanOutDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
		Reranker: s.reranker, Weights: matching.DefaultWeights()}
	_, err := matching.FanOut(s.ctx, matching.FanOutInput{
		CanonicalID: "can_4", OpportunityID: "o4", Kind: "grant_d", Country: "TZ",
		Embedding: mpMakeUnitVector(1536, 3), FirstSeenAt: time.Now(),
	}, fanout)
	require.NoError(s.T(), err)

	gap := matching.GapFillDeps{KNN: s.knn, Store: s.store, EventLog: s.events,
		Reranker: s.reranker, Weights: matching.DefaultWeights()}
	_, err = matching.GapFill(s.ctx, matching.GapFillInput{
		CandidateID: "c4",
		Embedding:   mpMakeUnitVector(1536, 3),
		Kinds:       []string{"grant_d"},
		Countries:   []string{"TZ"},
		Since:       time.Now().Add(-time.Hour),
		MinScore:    0.3,
	}, gap)
	require.NoError(s.T(), err)

	var rowCnt int
	require.NoError(s.T(), s.db.QueryRowContext(s.ctx,
		`SELECT count(*) FROM candidate_matches WHERE candidate_id='c4'`).Scan(&rowCnt))
	require.Equal(s.T(), 1, rowCnt)
}

// ----- helpers -----

func (s *MatchingPipelineSuite) seedCandidate(id string, emb []float32, minScore float64, kinds, countries []string) {
	require.NoError(s.T(), s.idx.Upsert(s.ctx, matching.CandidateIndex{
		CandidateID: id, Embedding: emb, MinScore: minScore,
		DailyCap: 25, WeeklyCap: 100,
		Kinds: kinds, Countries: countries, Enabled: true,
	}))
}

func (s *MatchingPipelineSuite) seedOpportunity(id string, emb []float32, kind, country string) {
	_, err := s.db.ExecContext(s.ctx, `
		INSERT INTO opportunities (id, posted_at, status, hidden, embedding, kind, country, first_seen_at)
		VALUES ($1, now(), 'active', false, $2::vector, $3, $4, now())
		ON CONFLICT (id) DO NOTHING
	`, id, mpVectorLit(emb), kind, country)
	require.NoError(s.T(), err)
}

// mpMakeUnitVector returns a unit vector of length dim with a 1 at the given axis.
func mpMakeUnitVector(dim, axis int) []float32 {
	v := make([]float32, dim)
	if axis >= 0 && axis < dim {
		v[axis] = 1
	} else {
		v[0] = 1
	}
	return v
}

// mpVectorLit serialises a float32 slice to pgvector literal form, e.g. "[1,0,0]".
func mpVectorLit(v []float32) string {
	out := "["
	for i, f := range v {
		if i > 0 {
			out += ","
		}
		out += strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return out + "]"
}
