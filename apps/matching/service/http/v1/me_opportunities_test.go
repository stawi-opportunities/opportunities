package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

type fakeFeedStore struct {
	page     matching.ListOpportunitiesPage
	err      error
	lastArgs matching.ListOpportunitiesParams
}

func (f *fakeFeedStore) ListOpportunitiesForCandidate(_ context.Context, p matching.ListOpportunitiesParams) (matching.ListOpportunitiesPage, error) {
	f.lastArgs = p
	return f.page, f.err
}

func TestOpportunitiesHandler_DefaultFilter(t *testing.T) {
	t.Parallel()
	store := &fakeFeedStore{page: matching.ListOpportunitiesPage{
		Items: []matching.OpportunityFeedItem{
			{OpportunityID: "opp_a", ApplyURL: "https://example.test/apply/a", Score: 0.9, Starred: true, CreatedAt: time.Unix(1, 0)},
			{OpportunityID: "opp_b", ApplyURL: "https://example.test/apply/b", Score: 0.6, Application: &matching.ApplicationSummary{
				Status: "applied", AppliedAt: time.Unix(2, 0), Method: "manual",
			}, CreatedAt: time.Unix(2, 0)},
		},
		NextCursor: "next-cur",
	}}
	h := httpmw.NewCandidateAuth(nil)(v1.OpportunitiesHandler(v1.OpportunitiesDeps{Store: store}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me/opportunities", nil)
	req.Header.Set("X-Candidate-ID", "cand_y")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []struct {
			OpportunityID string  `json:"opportunity_id"`
			ApplyURL      string  `json:"apply_url"`
			Score         float64 `json:"score,omitempty"`
			Starred       bool    `json:"starred"`
			Application   *struct {
				Status string `json:"status"`
			} `json:"application,omitempty"`
		} `json:"items"`
		NextCursor string `json:"next_cursor,omitempty"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	require.Equal(t, "opp_a", body.Items[0].OpportunityID)
	require.Equal(t, "https://example.test/apply/a", body.Items[0].ApplyURL)
	require.True(t, body.Items[0].Starred)
	require.Nil(t, body.Items[0].Application)
	require.Equal(t, "opp_b", body.Items[1].OpportunityID)
	require.NotNil(t, body.Items[1].Application)
	require.Equal(t, "applied", body.Items[1].Application.Status)
	require.Equal(t, "next-cur", body.NextCursor)
	require.Equal(t, matching.FilterAll, store.lastArgs.Filter)
}

func TestOpportunitiesHandler_FilterPropagates(t *testing.T) {
	t.Parallel()
	store := &fakeFeedStore{}
	h := httpmw.NewCandidateAuth(nil)(v1.OpportunitiesHandler(v1.OpportunitiesDeps{Store: store}))

	for raw, want := range map[string]matching.FeedFilter{
		"":        matching.FilterAll,
		"all":     matching.FilterAll,
		"matches": matching.FilterMatches,
		"starred": matching.FilterStarred,
		"applied": matching.FilterApplied,
		"bogus":   matching.FilterAll, // unknown filters fall back to all
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/me/opportunities?filter="+raw, nil)
		req.Header.Set("X-Candidate-ID", "cand_y")
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "filter=%q", raw)
		require.Equal(t, want, store.lastArgs.Filter, "filter=%q", raw)
	}
}

func TestOpportunitiesHandler_StoreErrorIs502(t *testing.T) {
	t.Parallel()
	store := &fakeFeedStore{err: errors.New("db wedged")}
	h := httpmw.NewCandidateAuth(nil)(v1.OpportunitiesHandler(v1.OpportunitiesDeps{Store: store}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me/opportunities", nil)
	req.Header.Set("X-Candidate-ID", "cand_y")
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadGateway, rec.Code)
}
