package v1

import (
	"context"
	"fmt"
	"sort"

	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// MatchPersister writes MatchService results into candidate_matches so the
// dashboard feed (which only reads that table) stays in sync with the
// legacy KNN path used by preference updates and on-demand match.
type MatchPersister interface {
	UpsertMatches(ctx context.Context, ms []matching.Match) error
}

// WeekCounter returns non-overflow matches created in the last 7 days.
type WeekCounter interface {
	CountNonOverflowThisWeek(ctx context.Context, candidateID string) (int, error)
}

// PersistCaps limits how many new non-overflow rows a persist can write.
// WeeklyCap 0 = uncapped weekly. DailyCap 0 = ignore daily for this path.
type PersistCaps struct {
	DailyCap  int
	WeeklyCap int
	// WeekUsed is non-overflow count this week (from WeekCounter).
	WeekUsed int
	// DayUsed optional; when DailyCap > 0 and DayUsed >= DailyCap, all overflow.
	DayUsed int
}

// CapsFromEntitlements builds PersistCaps for a billing tier.
func CapsFromEntitlements(ent billing.Entitlements, weekUsed, dayUsed int) PersistCaps {
	return PersistCaps{
		DailyCap:  ent.DailyCap,
		WeeklyCap: ent.WeeklyCap,
		WeekUsed:  weekUsed,
		DayUsed:   dayUsed,
	}
}

// PersistMatchResult converts SearchHits into candidate_matches rows.
// When caps is non-nil, enforces remaining weekly (and optional daily) budget:
// excess rows are stored as overflow (hidden from default feed).
func PersistMatchResult(ctx context.Context, store MatchPersister, res MatchResult, path string, caps *PersistCaps) error {
	if store == nil || len(res.Matches) == 0 {
		return nil
	}
	batchID := res.MatchBatchID
	if batchID == "" {
		batchID = xid.New().String()
	}

	// Highest score first so remaining budget is spent on best fits.
	hits := append([]SearchHit(nil), res.Matches...)
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })

	remainingWeek := -1 // unlimited
	if caps != nil && caps.WeeklyCap > 0 {
		remainingWeek = caps.WeeklyCap - caps.WeekUsed
		if remainingWeek < 0 {
			remainingWeek = 0
		}
	}
	dayFull := caps != nil && caps.DailyCap > 0 && caps.DayUsed >= caps.DailyCap

	ms := make([]matching.Match, 0, len(hits))
	for _, h := range hits {
		oppID := h.CanonicalID
		if oppID == "" {
			continue
		}
		status := matching.StatusNew
		if dayFull {
			status = matching.StatusOverflow
		} else if remainingWeek == 0 {
			status = matching.StatusOverflow
		} else if remainingWeek > 0 {
			remainingWeek--
		}
		ms = append(ms, matching.Match{
			MatchID:       xid.New().String(),
			CandidateID:   res.CandidateID,
			OpportunityID: oppID,
			ApplyURL:      h.ApplyURL,
			Status:        status,
			Score:         h.Score,
			LastEventID:   batchID,
			Metadata: map[string]any{
				"path":    path,
				"title":   h.Title,
				"company": h.Company,
				"slug":    h.Slug,
			},
		})
	}
	if len(ms) == 0 {
		return nil
	}
	if err := store.UpsertMatches(ctx, ms); err != nil {
		return fmt.Errorf("persist matches: %w", err)
	}
	return nil
}

// LoadWeekUsed returns non-overflow matches this week, or 0 on error/nil.
func LoadWeekUsed(ctx context.Context, w WeekCounter, candidateID string) int {
	if w == nil || candidateID == "" {
		return 0
	}
	n, err := w.CountNonOverflowThisWeek(ctx, candidateID)
	if err != nil {
		return 0
	}
	return n
}
