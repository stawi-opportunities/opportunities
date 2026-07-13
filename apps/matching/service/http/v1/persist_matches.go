package v1

import (
	"context"
	"fmt"

	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// MatchPersister writes MatchService results into candidate_matches so the
// dashboard feed (which only reads that table) stays in sync with the
// legacy KNN path used by preference updates and on-demand match.
type MatchPersister interface {
	UpsertMatches(ctx context.Context, ms []matching.Match) error
}

// PersistMatchResult converts SearchHits into candidate_matches rows.
// Best-effort: callers log and continue on error so emit/notify still run.
func PersistMatchResult(ctx context.Context, store MatchPersister, res MatchResult, path string) error {
	if store == nil || len(res.Matches) == 0 {
		return nil
	}
	batchID := res.MatchBatchID
	if batchID == "" {
		batchID = xid.New().String()
	}
	ms := make([]matching.Match, 0, len(res.Matches))
	for _, h := range res.Matches {
		oppID := h.CanonicalID
		if oppID == "" {
			continue
		}
		ms = append(ms, matching.Match{
			MatchID:       xid.New().String(),
			CandidateID:   res.CandidateID,
			OpportunityID: oppID,
			ApplyURL:      h.ApplyURL,
			Status:        matching.StatusNew,
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
