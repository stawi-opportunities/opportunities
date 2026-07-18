package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// OpportunityFeedStore is the read surface the handler depends on —
// defined here instead of importing matching.Store directly so tests
// can pass an in-memory fake without spinning a real DB.
type OpportunityFeedStore interface {
	ListOpportunitiesForCandidate(ctx context.Context, p matching.ListOpportunitiesParams) (matching.ListOpportunitiesPage, error)
}

type OpportunitiesDeps struct {
	Store OpportunityFeedStore
}

type feedItemDTO struct {
	// MatchID is present when this row is (or joins) a candidate_matches row.
	// Clients use it for dismiss; omit when empty.
	MatchID       string                 `json:"match_id,omitempty"`
	OpportunityID string                 `json:"opportunity_id"`
	ApplyURL      string                 `json:"apply_url"`
	Score         float64                `json:"score,omitempty"`
	Starred       bool                   `json:"starred"`
	Application   *applicationSummaryDTO `json:"application,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	// Card enrichment from opportunities join (avoids public slug lookup by id).
	Slug          string     `json:"slug,omitempty"`
	Title         string     `json:"title,omitempty"`
	Kind          string     `json:"kind,omitempty"`
	Company       string     `json:"company,omitempty"`
	Country       string     `json:"country,omitempty"`
	Region        string     `json:"region,omitempty"`
	City          string     `json:"city,omitempty"`
	Remote        bool       `json:"remote,omitempty"`
	PostedAt      *time.Time `json:"posted_at,omitempty"`
	SalaryMin     *float64   `json:"salary_min,omitempty"`
	SalaryMax     *float64   `json:"salary_max,omitempty"`
	Currency      string     `json:"currency,omitempty"`
	HasHowToApply bool       `json:"has_how_to_apply,omitempty"`
}

type applicationSummaryDTO struct {
	Status      string    `json:"status"`
	AppliedAt   time.Time `json:"applied_at"`
	LastEventAt time.Time `json:"last_event_at"`
	Method      string    `json:"method"`
}

type feedPageDTO struct {
	Items      []feedItemDTO `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// OpportunitiesHandler serves GET /me/opportunities — the dashboard's
// unified feed.
func OpportunitiesHandler(deps OpportunitiesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		filter := parseFilter(r.URL.Query().Get("filter"))
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}

		page, err := deps.Store.ListOpportunitiesForCandidate(ctx, matching.ListOpportunitiesParams{
			CandidateID: candidateID,
			Filter:      filter,
			Cursor:      r.URL.Query().Get("cursor"),
			Limit:       limit,
		})
		if err != nil {
			log.WithError(err).WithField("candidate_id", candidateID).WithField("filter", string(filter)).
				Error("me/opportunities: list failed")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "feed_lookup_failed", "could not load opportunities feed")
			return
		}

		out := feedPageDTO{
			Items:      make([]feedItemDTO, 0, len(page.Items)),
			NextCursor: page.NextCursor,
		}
		for _, it := range page.Items {
			dto := feedItemDTO{
				MatchID:       it.MatchID,
				OpportunityID: it.OpportunityID,
				ApplyURL:      it.ApplyURL,
				Score:         it.Score,
				Starred:       it.Starred,
				CreatedAt:     it.CreatedAt,
				Slug:          it.Slug,
				Title:         it.Title,
				Kind:          it.Kind,
				Company:       it.IssuingEntity,
				Country:       it.Country,
				Region:        it.Region,
				City:          it.City,
				Remote:        it.Remote,
				PostedAt:      it.PostedAt,
				SalaryMin:     it.AmountMin,
				SalaryMax:     it.AmountMax,
				Currency:      it.Currency,
				HasHowToApply: it.HasHowToApply,
			}
			if it.Application != nil {
				dto.Application = &applicationSummaryDTO{
					Status:      it.Application.Status,
					AppliedAt:   it.Application.AppliedAt,
					LastEventAt: it.Application.LastEventAt,
					Method:      it.Application.Method,
				}
			}
			out.Items = append(out.Items, dto)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func parseFilter(s string) matching.FeedFilter {
	switch s {
	case string(matching.FilterMatches):
		return matching.FilterMatches
	case string(matching.FilterStarred):
		return matching.FilterStarred
	case string(matching.FilterApplied):
		return matching.FilterApplied
	default: // empty or unknown
		return matching.FilterAll
	}
}
