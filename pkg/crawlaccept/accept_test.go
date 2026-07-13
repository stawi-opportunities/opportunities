package crawlaccept_test

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/crawlaccept"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

func jobRegistry(t *testing.T) *opportunity.Registry {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// pkg/crawlaccept → repo root → definitions/opportunity-kinds
	dir := filepath.Join(filepath.Dir(file), "..", "..", "definitions", "opportunity-kinds")
	reg, err := opportunity.LoadFromDir(dir)
	require.NoError(t, err)
	return reg
}

func testSource() *domain.Source {
	src := &domain.Source{
		Type:     domain.SourceAPI,
		Country:  "KE",
		Language: "en",
		Kinds:    []string{"job"},
		BaseURL:  "https://board.example.com/",
	}
	src.ID = "src_1"
	return src
}

func TestAccept_ValidJob_ExtractedCountryWins(t *testing.T) {
	t.Parallel()
	desc := "This is a long enough description for the verify gate to accept the job posting fully."
	res := crawlaccept.Accept(crawlaccept.Input{
		Opp: domain.ExternalOpportunity{
			Kind:          "job",
			Title:         "Backend Engineer",
			IssuingEntity: "Acme",
			Description:   desc,
			ApplyURL:      "https://jobs.example.com/postings/abc-123",
			ExternalID:    "abc-123",
			AnchorLocation: &domain.Location{
				Country: "UG", City: "Kampala",
			},
		},
		Source: testSource(),
		Kinds:  jobRegistry(t),
		Now:    func() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) },
	})
	require.Nil(t, res.Rejected)
	require.NotNil(t, res.Accepted)
	require.Equal(t, "Backend Engineer", res.Accepted.Title)
	require.Equal(t, "UG", res.Accepted.AnchorCountry, "extracted country must win over source KE")
	require.Equal(t, "Kampala", res.Accepted.AnchorCity)
	require.NotEmpty(t, res.Accepted.HardKey)
	require.Equal(t, "https://jobs.example.com/postings/abc-123", res.Accepted.ApplyURL)
}

func TestAccept_RejectsMissingDescription(t *testing.T) {
	t.Parallel()
	res := crawlaccept.Accept(crawlaccept.Input{
		Opp: domain.ExternalOpportunity{
			Title: "Short", IssuingEntity: "Acme",
			Description: "too short",
			ApplyURL:    "https://jobs.example.com/x",
		},
		Source: testSource(),
		Kinds:  jobRegistry(t),
	})
	require.NotNil(t, res.Rejected)
	require.Nil(t, res.Accepted)
	require.Equal(t, "missing_description", res.Rejected.Reason)
}

func TestAccept_DoesNotUseBaseURLAsApply(t *testing.T) {
	t.Parallel()
	// No apply_url and no source_url → reject; never invent board BaseURL.
	res := crawlaccept.Accept(crawlaccept.Input{
		Opp: domain.ExternalOpportunity{
			Title: "Engineer", IssuingEntity: "Acme",
			Description: "This is a long enough description for the verify gate to accept the job posting fully.",
		},
		Source: testSource(),
		Kinds:  jobRegistry(t),
	})
	require.NotNil(t, res.Rejected)
	require.Equal(t, "missing_apply_url", res.Rejected.Reason)
}

func TestPrepare_FillsKindAndCountry(t *testing.T) {
	t.Parallel()
	src := &domain.Source{Country: "TZ", Kinds: []string{"job"}}
	opp := domain.ExternalOpportunity{SourceURL: "https://x.test/jobs/1"}
	crawlaccept.Prepare(&opp, src)
	require.Equal(t, "job", opp.Kind)
	require.Equal(t, "https://x.test/jobs/1", opp.ApplyURL)
	require.NotNil(t, opp.AnchorLocation)
	require.Equal(t, "TZ", opp.AnchorLocation.Country)
}
