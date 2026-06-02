package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type fakeCandidateReader struct {
	candidate *domain.CandidateProfile
	err       error
	calledID  string
}

func (f *fakeCandidateReader) GetByID(_ context.Context, id string) (*domain.CandidateProfile, error) {
	f.calledID = id
	return f.candidate, f.err
}

type fakeMatchSummarizer struct {
	queued    int
	delivered int
	err       error
	calledID  string
}

func (f *fakeMatchSummarizer) SubscriptionSummary(_ context.Context, candidateID string) (int, int, error) {
	f.calledID = candidateID
	return f.queued, f.delivered, f.err
}

// withCandidate returns an *http.Request as if it had passed through
// httpmw.CandidateAuth — i.e. the candidate ID header is set so the
// inner handler can read it from context after wrapping.
func withCandidate(t *testing.T, candidateID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/me/subscription", nil)
	req.Header.Set("X-Candidate-ID", candidateID)
	return req
}

func TestSubscriptionHandler_StatusMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		candidate      *domain.CandidateProfile
		wantStatus     string
		wantPlanIsNull bool
		wantPlanValue  string
	}{
		{
			name:           "free → none, no plan",
			candidate:      &domain.CandidateProfile{Subscription: domain.SubscriptionFree, PlanID: ""},
			wantStatus:     "none",
			wantPlanIsNull: true,
		},
		{
			name:          "trial → active, plan surfaced",
			candidate:     &domain.CandidateProfile{Subscription: domain.SubscriptionTrial, PlanID: "starter"},
			wantStatus:    "active",
			wantPlanValue: "starter",
		},
		{
			name:          "paid → active",
			candidate:     &domain.CandidateProfile{Subscription: domain.SubscriptionPaid, PlanID: "pro"},
			wantStatus:    "active",
			wantPlanValue: "pro",
		},
		{
			name:          "cancelled → cancelled (plan retained for renewal CTA)",
			candidate:     &domain.CandidateProfile{Subscription: domain.SubscriptionCancelled, PlanID: "pro"},
			wantStatus:    "cancelled",
			wantPlanValue: "pro",
		},
		{
			name:           "missing candidate → none with null plan",
			candidate:      nil,
			wantStatus:     "none",
			wantPlanIsNull: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candReader := &fakeCandidateReader{candidate: tc.candidate}
			matches := &fakeMatchSummarizer{queued: 3, delivered: 7}
			h := httpmw.CandidateAuth(v1.SubscriptionHandler(v1.SubscriptionDeps{
				Candidates: candReader,
				Matches:    matches,
			}))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, withCandidate(t, "cand_1"))

			require.Equal(t, http.StatusOK, rec.Code)
			var got v1.MeSubscription
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, tc.wantStatus, got.Status)
			if tc.wantPlanIsNull {
				require.Nil(t, got.Plan)
			} else {
				require.NotNil(t, got.Plan)
				require.Equal(t, tc.wantPlanValue, *got.Plan)
			}
			require.Equal(t, 3, got.QueuedMatches)
			require.Equal(t, 7, got.DeliveredThisWeek)
			require.Equal(t, "cand_1", candReader.calledID)
			require.Equal(t, "cand_1", matches.calledID)
		})
	}
}

func TestSubscriptionHandler_CandidateLookupErrorReturnsProblemJSON(t *testing.T) {
	t.Parallel()
	candReader := &fakeCandidateReader{err: errors.New("db down")}
	h := httpmw.CandidateAuth(v1.SubscriptionHandler(v1.SubscriptionDeps{
		Candidates: candReader,
		Matches:    &fakeMatchSummarizer{},
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withCandidate(t, "cand_lookup_err"))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), "candidate_lookup_failed")
}

func TestSubscriptionHandler_NilMatchesDegradesToZeroCounts(t *testing.T) {
	t.Parallel()
	// Deployments without the Phase-4 matching stack pass nil for
	// Matches. The handler must still serve plan/status; counts
	// fall back to zero rather than 5xx.
	candReader := &fakeCandidateReader{
		candidate: &domain.CandidateProfile{Subscription: domain.SubscriptionPaid, PlanID: "pro"},
	}
	h := httpmw.CandidateAuth(v1.SubscriptionHandler(v1.SubscriptionDeps{
		Candidates: candReader,
		Matches:    nil,
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withCandidate(t, "cand_no_matches"))

	require.Equal(t, http.StatusOK, rec.Code)
	var got v1.MeSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "active", got.Status)
	require.Equal(t, 0, got.QueuedMatches)
	require.Equal(t, 0, got.DeliveredThisWeek)
}

func TestSubscriptionHandler_SummaryErrorReturnsZeroCountsNotFailure(t *testing.T) {
	t.Parallel()
	// A wedged metrics query shouldn't bring down the panel — the
	// dashboard already tolerates zero counts via its own fallback.
	candReader := &fakeCandidateReader{
		candidate: &domain.CandidateProfile{Subscription: domain.SubscriptionPaid, PlanID: "pro"},
	}
	matches := &fakeMatchSummarizer{err: errors.New("connection refused")}
	h := httpmw.CandidateAuth(v1.SubscriptionHandler(v1.SubscriptionDeps{
		Candidates: candReader,
		Matches:    matches,
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withCandidate(t, "cand_summary_err"))

	require.Equal(t, http.StatusOK, rec.Code)
	var got v1.MeSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "active", got.Status)
	require.Equal(t, 0, got.QueuedMatches)
	require.Equal(t, 0, got.DeliveredThisWeek)
}

func TestSubscriptionHandler_BlankPlanRendersAsNullJSON(t *testing.T) {
	t.Parallel()
	candReader := &fakeCandidateReader{
		candidate: &domain.CandidateProfile{Subscription: domain.SubscriptionPaid, PlanID: "   "},
	}
	h := httpmw.CandidateAuth(v1.SubscriptionHandler(v1.SubscriptionDeps{
		Candidates: candReader,
		Matches:    &fakeMatchSummarizer{},
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withCandidate(t, "cand_blank_plan"))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"plan":null`)
}
