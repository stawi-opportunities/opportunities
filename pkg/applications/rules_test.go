package applications_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestRules_Defaults(t *testing.T) {
	r := applications.DefaultRules()
	require.Equal(t, 1, r.Version)
	require.True(t, r.Enabled)
	require.Equal(t, []string{"job"}, r.Kinds)
	require.False(t, r.Autoapply.Enabled)
}

func TestRules_ValidMinimal(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"min_score":0.6,"kinds":["job"]}`)
	r, err := applications.ParseRules(body)
	require.NoError(t, err)
	require.Equal(t, []string{"job"}, r.Kinds)
	require.InDelta(t, 0.6, r.MinScore, 1e-9)
}

func TestRules_ValidFull(t *testing.T) {
	body := []byte(`{
		"version": 1,
		"enabled": true,
		"min_score": 0.62,
		"daily_cap": 25,
		"weekly_cap": 100,
		"kinds": ["job","scholarship"],
		"countries": ["KE","UG","TZ","remote"],
		"salary_floor_usd": 30000,
		"remote_only": false,
		"dismiss_after_days": 14,
		"blocklist": {"companies":["AcmeCo"],"domains":["example.com"]},
		"autoapply": {"enabled":true,"require_min_score":0.78,"kinds":["job"]}
	}`)
	r, err := applications.ParseRules(body)
	require.NoError(t, err)
	require.Equal(t, 25, r.DailyCap)
	require.True(t, r.Autoapply.Enabled)
	require.Contains(t, r.Blocklist.Companies, "AcmeCo")
}

func TestRules_RejectsUnknownField(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":["job"],"foo":"bar"}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "foo")
}

func TestRules_RejectsBadVersion(t *testing.T) {
	body := []byte(`{"version":99,"enabled":true,"kinds":["job"]}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_RejectsScoreOutOfRange(t *testing.T) {
	cases := []string{
		`{"version":1,"enabled":true,"kinds":["job"],"min_score":-0.1}`,
		`{"version":1,"enabled":true,"kinds":["job"],"min_score":1.5}`,
	}
	for _, body := range cases {
		_, err := applications.ParseRules([]byte(body))
		require.Error(t, err, "should reject %s", body)
	}
}

func TestRules_RejectsEmptyKinds(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":[]}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_RejectsInvalidKind(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":["unknown_kind"]}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_RejectsCapsOutOfBounds(t *testing.T) {
	body := []byte(`{"version":1,"enabled":true,"kinds":["job"],"daily_cap":0}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)

	body = []byte(`{"version":1,"enabled":true,"kinds":["job"],"daily_cap":10000}`)
	_, err = applications.ParseRules(body)
	require.Error(t, err)
}

func TestRules_AutoapplyMinScoreMustBeAtLeastMinScore(t *testing.T) {
	body := []byte(`{
		"version":1,"enabled":true,"kinds":["job"],
		"min_score":0.7,
		"autoapply":{"enabled":true,"require_min_score":0.5,"kinds":["job"]}
	}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "require_min_score")
}

func TestRules_AutoapplyKindsMustBeSubsetOfKinds(t *testing.T) {
	body := []byte(`{
		"version":1,"enabled":true,"kinds":["job"],
		"autoapply":{"enabled":true,"require_min_score":0.8,"kinds":["scholarship"]}
	}`)
	_, err := applications.ParseRules(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "autoapply.kinds")
}

func TestRules_RoundTrip(t *testing.T) {
	r := applications.DefaultRules()
	r.MinScore = 0.7
	r.Countries = []string{"KE", "remote"}
	body, err := applications.MarshalRules(r)
	require.NoError(t, err)
	parsed, err := applications.ParseRules(body)
	require.NoError(t, err)
	require.Equal(t, r, parsed)
}
