package v1

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// --- fakes ---

type fakeCandidateRepo struct {
	profile *domain.CandidateProfile
	err     error
}

func (f *fakeCandidateRepo) GetByID(_ context.Context, _ string) (*domain.CandidateProfile, error) {
	return f.profile, f.err
}

type fakeAutoAppRepo struct {
	todayCount int
	existing   map[string]bool
}

func (f *fakeAutoAppRepo) ExistsForCandidate(_ context.Context, cid, jid string) (bool, error) {
	return f.existing[cid+":"+jid], nil
}
func (f *fakeAutoAppRepo) CountTodayForCandidate(_ context.Context, _ string) (int, error) {
	return f.todayCount, nil
}

type fakePublisher struct {
	published [][]byte
}

func (f *fakePublisher) Publish(_ context.Context, _ string, data []byte) error {
	f.published = append(f.published, data)
	return nil
}

func buildHandler(
	profile *domain.CandidateProfile,
	todayCount int,
	existing map[string]bool,
	scoreMin float64,
	dailyLimit int,
	enabled bool,
) (*AutoApplyTriggerHandler, *fakePublisher) {
	pub := &fakePublisher{}
	h := &AutoApplyTriggerHandler{
		deps: AutoApplyTriggerDeps{
			Svc:           nil,
			CandidateRepo: &fakeCandidateRepo{profile: profile},
			AppRepo:       &fakeAutoAppRepo{todayCount: todayCount, existing: existing},
			ScoreMin:      scoreMin,
			DailyLimit:    dailyLimit,
			Enabled:       enabled,
		},
	}
	h.deps.PublishFn = pub.Publish
	return h, pub
}

const testApplyURL = "https://boards.greenhouse.io/co/jobs/1"

func buildMatchesPayload(t *testing.T, candidateID string, matches []eventsv1.MatchRow) *json.RawMessage {
	t.Helper()
	env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
		CandidateID:  candidateID,
		MatchBatchID: "batch_1",
		Matches:      matches,
	})
	b, err := json.Marshal(env)
	require.NoError(t, err)
	raw := json.RawMessage(b)
	return &raw
}

func eligibleProfile() *domain.CandidateProfile {
	return &domain.CandidateProfile{
		AutoApply:    true,
		Status:       domain.CandidateActive,
		Subscription: domain.SubscriptionPaid,
		CurrentTitle: "Software Engineer",
		CVUrl:        "https://r2.example.com/cv.pdf",
	}
}

// --- tests ---

func TestAutoApplyTrigger_Disabled(t *testing.T) {
	h, pub := buildHandler(eligibleProfile(), 0, nil, 0.75, 5, false)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.9}})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Empty(t, pub.published)
}

func TestAutoApplyTrigger_IneligibleCandidate_AutoApplyFalse(t *testing.T) {
	profile := eligibleProfile()
	profile.AutoApply = false
	h, pub := buildHandler(profile, 0, nil, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.9}})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Empty(t, pub.published)
}

func TestAutoApplyTrigger_IneligibleCandidate_NotPaid(t *testing.T) {
	profile := eligibleProfile()
	profile.Subscription = domain.SubscriptionFree
	h, pub := buildHandler(profile, 0, nil, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.9}})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Empty(t, pub.published)
}

func TestAutoApplyTrigger_ScoreBelowThreshold(t *testing.T) {
	h, pub := buildHandler(eligibleProfile(), 0, nil, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.5}})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Empty(t, pub.published)
}

func TestAutoApplyTrigger_DailyLimitReached(t *testing.T) {
	h, pub := buildHandler(eligibleProfile(), 5, nil, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.9}})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Empty(t, pub.published)
}

func TestAutoApplyTrigger_AlreadyApplied(t *testing.T) {
	existing := map[string]bool{"cnd_1:job_1": true}
	h, pub := buildHandler(eligibleProfile(), 0, existing, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.9}})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Empty(t, pub.published)
}

func TestAutoApplyTrigger_HappyPath(t *testing.T) {
	h, pub := buildHandler(eligibleProfile(), 0, nil, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{
		{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.9},
		{CanonicalID: "job_2", ApplyURL: "https://jobs.lever.co/company/456", Score: 0.8},
	})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Len(t, pub.published, 2)

	// Verify the first intent has the expected fields.
	var env eventsv1.Envelope[eventsv1.AutoApplyIntentV1]
	require.NoError(t, json.Unmarshal(pub.published[0], &env))
	assert.Equal(t, "cnd_1", env.Payload.CandidateID)
	assert.Equal(t, "job_1", env.Payload.CanonicalJobID)
	assert.Equal(t, testApplyURL, env.Payload.ApplyURL)
	assert.Equal(t, "greenhouse", env.Payload.SourceType)
	assert.Equal(t, 0.9, env.Payload.Score)
}

func TestAutoApplyTrigger_DailyLimitPartial(t *testing.T) {
	// 4 applied today, limit 5 → only 1 more allowed even with 3 matches.
	h, pub := buildHandler(eligibleProfile(), 4, nil, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{
		{CanonicalID: "job_1", ApplyURL: testApplyURL, Score: 0.95},
		{CanonicalID: "job_2", ApplyURL: testApplyURL, Score: 0.90},
		{CanonicalID: "job_3", ApplyURL: testApplyURL, Score: 0.85},
	})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Len(t, pub.published, 1)
}

func TestAutoApplyTrigger_EmptyApplyURL_Skipped(t *testing.T) {
	h, pub := buildHandler(eligibleProfile(), 0, nil, 0.75, 5, true)
	raw := buildMatchesPayload(t, "cnd_1", []eventsv1.MatchRow{{CanonicalID: "job_1", ApplyURL: "", Score: 0.9}})
	require.NoError(t, h.Execute(context.Background(), raw))
	assert.Empty(t, pub.published)
}

func TestDeriveSourceType(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://boards.greenhouse.io/acme/jobs/123", "greenhouse"},
		{"https://jobs.lever.co/company/456", "lever"},
		{"https://acme.myworkdayjobs.com/en-US/External", "workday"},
		{"https://careers.smartrecruiters.com/co/role-id", "smartrecruiters_page"},
		{"https://example.com/jobs/123", ""},
	}
	for _, c := range cases {
		got := deriveSourceType(c.url)
		assert.Equal(t, c.want, got, "URL: %s", c.url)
	}
}
