package matching

import (
	"context"
	"fmt"
	"sort"
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
	// DailyCap optional — when set with input DailyCap > 0, excess rows are overflow.
	DailyCap dailyCapCounter
	// WeekCount optional — when set with WeeklyCap > 0, enforces remaining weekly budget.
	WeekCount weekMatchCounter
	Now       func() time.Time
	NewID     func() string
}

type dailyCapCounter interface {
	TodayCount(ctx context.Context, candidateID string) (int, error)
}

// weekMatchCounter returns non-overflow matches created in the last 7 days.
type weekMatchCounter interface {
	CountNonOverflowThisWeek(ctx context.Context, candidateID string) (int, error)
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
	// DailyCap / WeeklyCap enforce plan entitlements. 0 = uncapped.
	// DailyCap uses deps.DailyCap when non-nil.
	// WeeklyCap uses deps.WeekCount remaining budget when non-nil; otherwise
	// truncates this run to WeeklyCap (legacy behaviour).
	DailyCap  int
	WeeklyCap int
	// QueryText is the candidate-side text (CV summary / skills) used as the
	// cross-encoder query. Empty disables reranking for this run.
	QueryText string
}

// GapFill reason codes for product empty-states (honest UX).
const (
	GapReasonOK             = "ok"
	GapReasonNoInventory    = "no_inventory"
	GapReasonBelowThreshold = "below_threshold"
	GapReasonWeeklyCap      = "weekly_cap"
	GapReasonDailyCap       = "daily_cap"
)

// GapFillResult summarises the read-path execution.
type GapFillResult struct {
	RunID       string
	OppsScanned int
	// ScoredAboveMin is how many KNN hits cleared MinScore before caps.
	ScoredAboveMin int
	MatchesWritten int
	RerankerStatus string
	LatencyMS      int
	// Reason explains empty/partial runs for the dashboard (ok|no_inventory|…).
	Reason string
	// WeeklyUsed / WeeklyCap echo plan usage when WeeklyCap > 0.
	WeeklyUsed int
	WeeklyCap  int
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
		res.Cosine = CosineFromPGDistance(h.Distance)
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
		rerankIn = append(rerankIn, RerankItem{ID: h.OpportunityID, Text: h.Text, Score: res.Total})
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

	// Plan caps: keep highest scores; enforce remaining weekly budget honestly.
	sort.Slice(scoredHits, func(i, j int) bool { return scoredHits[i].tot > scoredHits[j].tot })
	scoredAbove := len(scoredHits)

	weekUsed := 0
	if in.WeeklyCap > 0 {
		if deps.WeekCount != nil {
			if n, wErr := deps.WeekCount.CountNonOverflowThisWeek(ctx, in.CandidateID); wErr == nil {
				weekUsed = n
			}
		}
		remaining := in.WeeklyCap - weekUsed
		if remaining <= 0 {
			runEvt.Status = "ok"
			runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
			finished := now()
			runEvt.FinishedAt = &finished
			return GapFillResult{
				RunID:          runID,
				OppsScanned:    len(hits),
				ScoredAboveMin: scoredAbove,
				MatchesWritten: 0,
				RerankerStatus: runEvt.RerankerStatus,
				LatencyMS:      runEvt.LatencyMS,
				Reason:         GapReasonWeeklyCap,
				WeeklyUsed:     weekUsed,
				WeeklyCap:      in.WeeklyCap,
			}, nil
		}
		if len(scoredHits) > remaining {
			scoredHits = scoredHits[:remaining]
		}
	}

	todayUsed := 0
	if deps.DailyCap != nil && in.DailyCap > 0 {
		if n, cErr := deps.DailyCap.TodayCount(ctx, in.CandidateID); cErr == nil {
			todayUsed = n
		}
	}
	// If daily budget is fully spent, do not write more non-overflow rows.
	if in.DailyCap > 0 && todayUsed >= in.DailyCap && len(scoredHits) > 0 {
		// Still allow writing as overflow for audit, but product counts zero delivered.
		// Prefer empty result with reason so free users understand the limit.
		runEvt.Status = "ok"
		runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
		finished := now()
		runEvt.FinishedAt = &finished
		return GapFillResult{
			RunID:          runID,
			OppsScanned:    len(hits),
			ScoredAboveMin: scoredAbove,
			MatchesWritten: 0,
			RerankerStatus: runEvt.RerankerStatus,
			LatencyMS:      runEvt.LatencyMS,
			Reason:         GapReasonDailyCap,
			WeeklyUsed:     weekUsed,
			WeeklyCap:      in.WeeklyCap,
		}, nil
	}

	matches := make([]Match, 0, len(scoredHits))
	for i, s := range scoredHits {
		status := StatusNew
		if in.DailyCap > 0 && todayUsed+i >= in.DailyCap {
			status = StatusOverflow
		}
		var rp *float64
		if v, ok := rerankByID[s.hit.OpportunityID]; ok {
			rp = &v
		}
		matches = append(matches, Match{
			MatchID:       idgen(),
			CandidateID:   in.CandidateID,
			OpportunityID: s.hit.OpportunityID,
			Status:        status,
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
	// Count only non-overflow as "written" for product UX.
	written := 0
	for _, m := range matches {
		if m.Status != StatusOverflow {
			written++
		}
	}
	runEvt.MatchesWritten = written

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

	var reason string
	switch {
	case written > 0:
		reason = GapReasonOK
	case len(hits) == 0:
		reason = GapReasonNoInventory
	case scoredAbove == 0:
		reason = GapReasonBelowThreshold
	case in.DailyCap > 0 && todayUsed >= in.DailyCap:
		// Scored hits existed but none written as non-overflow.
		reason = GapReasonDailyCap
	default:
		reason = GapReasonBelowThreshold
	}

	runEvt.Status = "ok"
	runEvt.LatencyMS = int(time.Since(startedAt).Milliseconds())
	finished := now()
	runEvt.FinishedAt = &finished
	return GapFillResult{
		RunID:          runID,
		OppsScanned:    len(hits),
		ScoredAboveMin: scoredAbove,
		MatchesWritten: written,
		RerankerStatus: runEvt.RerankerStatus,
		LatencyMS:      runEvt.LatencyMS,
		Reason:         reason,
		WeeklyUsed:     weekUsed + written,
		WeeklyCap:      in.WeeklyCap,
	}, nil
}
