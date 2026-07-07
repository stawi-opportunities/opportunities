package recipe

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const detailPage = `<!DOCTYPE html><html><head>
<meta property="og:image" content="https://cdn.x.io/logo.png">
<script type="application/ld+json">
{"@type":"JobPosting","title":"Senior Go Engineer","description":"We need a strong Go engineer to build distributed systems and own delivery end to end.",
"hiringOrganization":{"name":"ACME","logo":"https://cdn.x.io/acme.png","description":"ACME builds things."},
"url":"https://x.io/jobs/senior-go","datePosted":"2026-06-01","jobLocation":{"address":{"addressCountry":"KE"}},
"baseSalary":{"value":{"minValue":1000,"maxValue":2000,"currency":"USD"}}}
</script></head><body><h1 itemprop="title">Senior Go Engineer</h1></body></html>`

func jobDetailRule() DetailRule {
	jl := func(path string) FieldExtractor { return FieldExtractor{From: []string{"json_ld"}, JSONPath: path} }
	return DetailRule{
		RecordSource:   "json_ld",
		Title:          jl("$.title"),
		Description:    jl("$.description"),
		IssuingEntity:  jl("$.hiringOrganization.name"),
		ApplyURL:       jl("$.url"),
		AnchorCountry:  jl("$.jobLocation.address.addressCountry"),
		PostedAt:       FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.datePosted", Transform: []string{"parse_date"}},
		AmountMin:      jl("$.baseSalary.value.minValue"),
		AmountMax:      jl("$.baseSalary.value.maxValue"),
		Currency:       jl("$.baseSalary.value.currency"),
		CompanyLogoURL: FieldExtractor{From: []string{"json_ld", "meta"}, JSONPath: "$.hiringOrganization.logo", Meta: "og:image"},
		CompanyProfile: jl("$.hiringOrganization.description"),
	}
}

func jobSource() domain.Source {
	s := domain.Source{Type: "brightermonday", BaseURL: "https://x.io", Country: "NG"}
	s.ID = "src_1"
	s.Kinds = []string{"job"}
	return s
}

func TestBuildOpportunity_MapsAllFields(t *testing.T) {
	pc, err := NewPageContext("https://x.io/jobs/senior-go", detailPage, nil)
	require.NoError(t, err)
	r := &Recipe{Acquisition: "structured_data", Kind: KindRule{Mode: "source_default"}, Detail: jobDetailRule()}

	opp, err := buildOpportunity(pc, jobSource(), r)
	require.NoError(t, err)

	assert.Equal(t, "job", opp.Kind)
	assert.Equal(t, "Senior Go Engineer", opp.Title)
	assert.Contains(t, opp.Description, "Go engineer")
	assert.Equal(t, "ACME", opp.IssuingEntity)
	assert.Equal(t, "https://x.io/jobs/senior-go", opp.ApplyURL)
	assert.Equal(t, "https://x.io/jobs/senior-go", opp.ExternalID)
	assert.Equal(t, "src_1", opp.SourceID)
	assert.EqualValues(t, "brightermonday", opp.Source)
	require.NotNil(t, opp.AnchorLocation)
	assert.Equal(t, "KE", opp.AnchorLocation.Country)
	require.NotNil(t, opp.PostedAt)
	assert.Equal(t, 2026, opp.PostedAt.Year())
	assert.Equal(t, 1000.0, opp.AmountMin)
	assert.Equal(t, 2000.0, opp.AmountMax)
	assert.Equal(t, "USD", opp.Currency)
	assert.Equal(t, "https://cdn.x.io/acme.png", opp.Attributes["company_logo_url"])
	assert.Equal(t, "ACME builds things.", opp.Attributes["company_profile"])
}

func TestBuildOpportunity_AnchorCountryFallsBackToSource(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"title":"x","description":"` +
		`a description long enough to clear the fifty character minimum gate.","url":"https://x.io/j/1"}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io/j/1", html, nil)
	require.NoError(t, err)
	r := &Recipe{Acquisition: "structured_data", Kind: KindRule{Mode: "source_default"}, Detail: jobDetailRule()}

	opp, err := buildOpportunity(pc, jobSource(), r)
	require.NoError(t, err)
	require.NotNil(t, opp.AnchorLocation)
	assert.Equal(t, "NG", opp.AnchorLocation.Country)
}

func TestBuildOpportunity_ApplyURLFallsBackToDetailPage(t *testing.T) {
	pc, err := NewPageContext("https://x.io/jobs/fallback", `<html><body><h1>Role</h1></body></html>`, nil)
	require.NoError(t, err)
	r := &Recipe{Kind: KindRule{Mode: "source_default"}, Detail: DetailRule{}}

	opp, err := buildOpportunity(pc, jobSource(), r)
	require.NoError(t, err)
	assert.Equal(t, "https://x.io/jobs/fallback", opp.ApplyURL)
	assert.Equal(t, opp.ApplyURL, opp.ExternalID)
}

func TestBuildOpportunity_KindFixedAndByPath(t *testing.T) {
	pc, err := NewPageContext("https://x.io/s/1",
		`<html><head><script type="application/ld+json">{"kind":"scholarship"}</script></head><body></body></html>`, nil)
	require.NoError(t, err)

	rFixed := &Recipe{Kind: KindRule{Mode: "fixed", Fixed: "tender"}, Detail: DetailRule{}}
	opp, err := buildOpportunity(pc, jobSource(), rFixed)
	require.NoError(t, err)
	assert.Equal(t, "tender", opp.Kind)

	rPath := &Recipe{Kind: KindRule{Mode: "by_path", Path: FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.kind"}}, Detail: DetailRule{}}
	opp, err = buildOpportunity(pc, jobSource(), rPath)
	require.NoError(t, err)
	assert.Equal(t, "scholarship", opp.Kind)
}

func TestBuildOpportunity_SourceDefaultMultiKindErrors(t *testing.T) {
	pc, _ := NewPageContext("https://x.io", "<html><body></body></html>", nil)
	src := jobSource()
	src.Kinds = []string{"job", "scholarship"}
	r := &Recipe{Kind: KindRule{Mode: "source_default"}, Detail: DetailRule{}}
	_, err := buildOpportunity(pc, src, r)
	require.Error(t, err)
}
