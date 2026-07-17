package matching

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/pitabwire/util"
)

// DailyCapQuery returns the number of 'generated' candidate_match_events
// rows for `candidateID` since the start of today (UTC).
type DailyCapQuery interface {
	TodayCount(ctx context.Context, candidateID string) (int, error)
}

// FanOutDeps is the minimal contract Path A needs.
type FanOutDeps struct {
	KNN      fanOutSearcher
	Store    matchUpserter
	EventLog matchEventWriter
	Reranker Reranker
	Weights  Weights
	Now      func() time.Time
	NewID    func() string // ID factory; defaults to xid-style hex
	// DailyCap is the optional query for today's per-candidate match count.
	// When nil (or unconfigured), the cap check is skipped — useful for
	// tests and for the bootstrap phase before the continuous aggregate
	// has any data.
	DailyCap DailyCapQuery
}

type fanOutSearcher interface {
	FanOutKNN(ctx context.Context, p FanOutKNNParams) ([]CandidateHit, error)
}
type matchUpserter interface {
	UpsertMatches(ctx context.Context, ms []Match) error
}
type matchEventWriter interface {
	WriteMatchEvent(ctx context.Context, m MatchEvent) error
	WriteMatchRunEvent(ctx context.Context, r MatchRunEvent) error
}

// FanOutInput is one CanonicalUpsertedV1 reduced to its matching-relevant fields.
type FanOutInput struct {
	CanonicalID   string
	OpportunityID string
	Kind          string
	Country       string
	SalaryMaxUSD  *int
	Embedding     []float32
	FirstSeenAt   time.Time
	Skills        []string
	// QueryText is the opportunity-side text (title + description) used as
	// the cross-encoder query. Empty disables reranking for this run.
	QueryText string
}

// FanOutResult summarises a single fan-out execution.
type FanOutResult struct {
	RunID             string
	CandidatesScanned int
	MatchesWritten    int
	Overflowed        int
	RerankerStatus    string
	LatencyMS         int
	// NewMatches are rows written with status=new (not overflow) — used
	// for optional per-match notifications.
	NewMatches []Match
}

// FanOut runs Path A end-to-end. Idempotent at the DB level (UpsertMatches
// + ON CONFLICT). Returns a summary suitable for telemetry.
func FanOut(ctx context.Context, in FanOutInput, deps FanOutDeps) (FanOutResult, error) {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	idgen := deps.NewID
	if idgen == nil {
		idgen = newHexID
	}

	runID := idgen()
	startedAt := now()
	runEvt := MatchRunEvent{
		RunID:       runID,
		StartedAt:   startedAt,
		Path:        PathFanout,
		TriggeredBy: "canonical_upserted",
		CanonicalID: in.CanonicalID,
	}
	defer func() {
		// Best-effort tail write. Errors here log but don't override
		// the caller's outcome.
		_ = deps.EventLog.WriteMatchRunEvent(ctx, runEvt)
	}()

	// 1. KNN retrieval. Limit hard-coded to 500 per spec §3.1.
	hits, err := deps.KNN.FanOutKNN(ctx, FanOutKNNParams{
		OppEmbedding: in.Embedding,
		OppKind:      in.Kind,
		OppCountry:   in.Country,
		OppSalaryMax: in.SalaryMaxUSD,
		Limit:        500,
	})
	if err != nil {
		runEvt.Status = "error"
		runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
		return FanOutResult{RunID: runID}, fmt.Errorf("matching: fanout knn: %w", err)
	}
	runEvt.CandidatesScanned = len(hits)

	if len(hits) == 0 {
		runEvt.Status = "ok"
		runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
		return FanOutResult{RunID: runID, CandidatesScanned: 0}, nil
	}

	// 2. Score each candidate using the deterministic Score function.
	type scored struct {
		hit   CandidateHit
		score Result
	}
	scoredHits := make([]scored, 0, len(hits))
	for _, h := range hits {
		// We don't have the candidate's full feature bag in the hit; the
		// hit carries only the filter columns (kinds/countries/salary
		// floor). Score from those + the opportunity's signals.
		candSig := CandidateSignal{
			Embedding:      nil, // distance is already the source of truth for the cosine term; we feed it in below
			Skills:         nil, // skills overlap is neutral here — fanout uses the index, not the raw CV
			Countries:      h.Countries,
			SalaryFloorUSD: h.SalaryFloorUSD,
		}
		oppSig := OpportunitySignal{
			Embedding:    in.Embedding,
			Skills:       in.Skills,
			Country:      in.Country,
			SalaryMaxUSD: in.SalaryMaxUSD,
			FirstSeenAt:  in.FirstSeenAt,
		}
		res := Score(candSig, oppSig, deps.Weights, now())
		// Override cosine from pgvector distance (shared helper).
		res.Cosine = CosineFromPGDistance(h.Distance)
		res.Total = deps.Weights.Cosine*res.Cosine +
			deps.Weights.Skills*res.SkillsOverlap +
			deps.Weights.Geo*res.GeoMatch +
			deps.Weights.Salary*res.SalaryFit -
			deps.Weights.Stale*res.StalePenalty
		if res.Total < 0 {
			res.Total = 0
		}
		if res.Total < h.MinScore {
			continue
		}
		scoredHits = append(scoredHits, scored{hit: h, score: res})
	}

	// 3. Reranker (best-effort).
	rerankIn := make([]RerankItem, len(scoredHits))
	for i, s := range scoredHits {
		rerankIn[i] = RerankItem{ID: s.hit.CandidateID, Text: s.hit.Text, Score: s.score.Total}
	}
	rerankOut, used, _ := deps.Reranker.Rerank(ctx, in.QueryText, rerankIn)
	rerankByID := map[string]float64{}
	if used {
		for _, r := range rerankOut {
			if r.RerankScore != nil {
				rerankByID[r.ID] = *r.RerankScore
			}
		}
		runEvt.RerankerStatus = "used"
	} else {
		runEvt.RerankerStatus = "skipped"
	}

	// 4. Daily-cap enforcement: rows beyond the candidate's daily_cap are
	// stored with status='overflow'. The cap is checked per-candidate via
	// the continuous aggregate on candidate_match_events (Phase 5).
	matches := make([]Match, 0, len(scoredHits))
	overflowCount := 0
	overflowEventKinds := map[string]EventKind{}
	for _, s := range scoredHits {
		status := StatusNew
		eventKind := EventKindGenerated
		if deps.DailyCap != nil && s.hit.DailyCap > 0 {
			todayCount, err := deps.DailyCap.TodayCount(ctx, s.hit.CandidateID)
			if err == nil && todayCount >= s.hit.DailyCap {
				status = StatusOverflow
				eventKind = EventKindOverflow
				runEvt.Status = "ok" // not an error; just overflow
				overflowCount++
			}
		}
		overflowEventKinds[s.hit.CandidateID] = eventKind

		var rerankPtr *float64
		if v, ok := rerankByID[s.hit.CandidateID]; ok {
			rerankPtr = &v
		}
		matches = append(matches, Match{
			MatchID:       idgen(),
			CandidateID:   s.hit.CandidateID,
			OpportunityID: in.OpportunityID,
			Status:        status,
			Score:         s.score.Total,
			RerankScore:   rerankPtr,
			// True only when the reranker both ran (`used`) AND produced
			// a score for this specific candidate (`rerankPtr != nil`).
			RerankerUsed: used && rerankPtr != nil,
			LastEventID:  runID,
			Metadata: map[string]any{
				"path":         "fanout",
				"canonical_id": in.CanonicalID,
				"kind":         in.Kind,
			},
		})
	}

	// 5. Bulk write.
	if err := deps.Store.UpsertMatches(ctx, matches); err != nil {
		runEvt.Status = "error"
		runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
		return FanOutResult{RunID: runID}, fmt.Errorf("matching: fanout upsert: %w", err)
	}
	runEvt.MatchesWritten = len(matches)
	newMatches := make([]Match, 0, len(matches))
	for _, m := range matches {
		if m.Status == StatusNew {
			newMatches = append(newMatches, m)
		}
	}

	// 6. Per-match events (audit trail — not user notifications).
	for _, m := range matches {
		rerankPtr := m.RerankScore
		kind := overflowEventKinds[m.CandidateID]
		if kind == "" {
			kind = EventKindGenerated
		}
		evt := MatchEvent{
			EventID:       idgen(),
			CandidateID:   m.CandidateID,
			OpportunityID: in.OpportunityID,
			CanonicalID:   in.CanonicalID,
			Kind:          kind,
			Path:          PathFanout,
			Score:         m.Score,
			RerankScore:   rerankPtr,
			RerankerUsed:  m.RerankerUsed,
			OccurredAt:    now(),
			Data: map[string]any{
				"run_id":   runID,
				"match_id": m.MatchID,
			},
		}
		if err := deps.EventLog.WriteMatchEvent(ctx, evt); err != nil {
			util.Log(ctx).WithError(err).
				WithField("event_id", evt.EventID).
				Warn("match event write failed (non-fatal)")
			// continue — the row in candidate_matches is the source of
			// truth; the event is the audit trail.
		}
	}

	runEvt.Status = "ok"
	runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
	finished := now()
	runEvt.FinishedAt = &finished
	return FanOutResult{
		RunID:             runID,
		CandidatesScanned: len(hits),
		MatchesWritten:    len(matches),
		Overflowed:        overflowCount,
		RerankerStatus:    runEvt.RerankerStatus,
		LatencyMS:         runEvt.LatencyMS,
		NewMatches:        newMatches,
	}, nil
}

func newHexID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
