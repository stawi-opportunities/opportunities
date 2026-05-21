package matching

import (
	"context"
	"fmt"
	"time"

	util "github.com/pitabwire/util"
)

// GapFillDeps mirrors FanOutDeps but uses ReverseKNN.
type GapFillDeps struct {
	KNN      reverseSearcher
	Store    matchUpserter
	EventLog matchEventWriter
	Reranker Reranker
	Weights  Weights
	Now      func() time.Time
	NewID    func() string
}

type reverseSearcher interface {
	ReverseKNN(ctx context.Context, p ReverseKNNParams) ([]OppHit, error)
}

// GapFillInput is one candidate's read-time refresh.
type GapFillInput struct {
	CandidateID    string
	Embedding      []float32
	Skills         []string
	Countries      []string
	Kinds          []string
	SalaryFloorUSD *int
	Since          time.Time
	MinScore       float64
}

// GapFillResult summarises the read-path execution.
type GapFillResult struct {
	RunID          string
	OppsScanned    int
	MatchesWritten int
	RerankerStatus string
	LatencyMS      int
}

// GapFill runs Path B: candidate-side discovery of new opportunities
// since the supplied cursor. Idempotent — UpsertMatches's conflict
// guard preserves any rows fan-out already wrote.
func GapFill(ctx context.Context, in GapFillInput, deps GapFillDeps) (GapFillResult, error) {
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
		Path:        PathGap,
		TriggeredBy: "extension_poll",
		CandidateID: in.CandidateID,
	}
	defer func() { _ = deps.EventLog.WriteMatchRunEvent(ctx, runEvt) }()

	hits, err := deps.KNN.ReverseKNN(ctx, ReverseKNNParams{
		CandidateEmbedding: in.Embedding,
		Kinds:              in.Kinds,
		Countries:          in.Countries,
		Since:              in.Since,
		Limit:              100,
	})
	if err != nil {
		runEvt.Status = "error"
		runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
		return GapFillResult{RunID: runID}, fmt.Errorf("matching: gapfill knn: %w", err)
	}
	runEvt.CandidatesScanned = 1 // a single candidate is in-scope for Path B

	type scored struct {
		hit OppHit
		tot float64
	}
	scoredHits := make([]scored, 0, len(hits))
	rerankIn := make([]RerankItem, 0, len(hits))
	for _, h := range hits {
		candSig := CandidateSignal{
			Embedding:      in.Embedding,
			Skills:         in.Skills,
			Countries:      in.Countries,
			SalaryFloorUSD: in.SalaryFloorUSD,
		}
		oppSig := OpportunitySignal{
			Embedding:   nil, // gap-fill uses the pgvector distance directly
			Skills:      nil,
			Country:     h.Country,
			FirstSeenAt: h.FirstSeenAt,
		}
		res := Score(candSig, oppSig, deps.Weights, now())
		res.Cosine = 1.0 - h.Distance/2.0
		if res.Cosine < 0 {
			res.Cosine = 0
		}
		res.Total = deps.Weights.Cosine*res.Cosine +
			deps.Weights.Skills*res.SkillsOverlap +
			deps.Weights.Geo*res.GeoMatch +
			deps.Weights.Salary*res.SalaryFit -
			deps.Weights.Stale*res.StalePenalty
		if res.Total < 0 {
			res.Total = 0
		}
		if res.Total < in.MinScore {
			continue
		}
		scoredHits = append(scoredHits, scored{hit: h, tot: res.Total})
		rerankIn = append(rerankIn, RerankItem{ID: h.OpportunityID, Score: res.Total})
	}

	rerankOut, used, _ := deps.Reranker.Rerank(ctx, rerankIn)
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

	matches := make([]Match, 0, len(scoredHits))
	for _, s := range scoredHits {
		var rp *float64
		if v, ok := rerankByID[s.hit.OpportunityID]; ok {
			rp = &v
		}
		matches = append(matches, Match{
			MatchID:       idgen(),
			CandidateID:   in.CandidateID,
			OpportunityID: s.hit.OpportunityID,
			Status:        StatusNew,
			Score:         s.tot,
			RerankScore:   rp,
			RerankerUsed:  used && rp != nil,
			LastEventID:   runID,
			Metadata:      map[string]any{"path": "gap"},
		})
	}

	if err := deps.Store.UpsertMatches(ctx, matches); err != nil {
		runEvt.Status = "error"
		runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
		return GapFillResult{RunID: runID}, fmt.Errorf("matching: gapfill upsert: %w", err)
	}
	runEvt.MatchesWritten = len(matches)

	for _, m := range matches {
		evt := MatchEvent{
			EventID:       idgen(),
			CandidateID:   m.CandidateID,
			OpportunityID: m.OpportunityID,
			Kind:          EventKindGenerated,
			Path:          PathGap,
			Score:         m.Score,
			RerankScore:   m.RerankScore,
			RerankerUsed:  m.RerankerUsed,
			OccurredAt:    now(),
			Data:          map[string]any{"run_id": runID, "match_id": m.MatchID},
		}
		if err := deps.EventLog.WriteMatchEvent(ctx, evt); err != nil {
			util.Log(ctx).WithError(err).
				WithField("event_id", evt.EventID).
				Warn("gapfill match event write failed (non-fatal)")
		}
	}

	runEvt.Status = "ok"
	runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
	finished := now()
	runEvt.FinishedAt = &finished
	return GapFillResult{
		RunID:          runID,
		OppsScanned:    len(hits),
		MatchesWritten: len(matches),
		RerankerStatus: runEvt.RerankerStatus,
		LatencyMS:      runEvt.LatencyMS,
	}, nil
}
