package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const samplePage = `<!DOCTYPE html><html><head>
<meta property="og:image" content="https://cdn.x.io/logo.png">
<meta name="description" content="Great job">
<script type="application/ld+json">
{"@type":"JobPosting","title":"Senior Go Engineer","hiringOrganization":{"name":"ACME","logo":"https://cdn.x.io/acme.png"}}
</script>
<script id="__NEXT_DATA__" type="application/json">
{"props":{"pageProps":{"job":{"slug":"senior-go"}}}}
</script>
</head><body>
<h1 itemprop="title">Senior Go Engineer</h1>
<a class="apply" href="/apply/1">Apply</a>
</body></html>`

func TestNewPageContext_HarvestsAllPlanes(t *testing.T) {
	pc, err := NewPageContext("https://x.io/jobs/senior-go", samplePage, nil)
	require.NoError(t, err)

	assert.Equal(t, "https://cdn.x.io/logo.png", pc.Meta["og:image"])
	assert.Equal(t, "Great job", pc.Meta["description"])

	require.Len(t, pc.JSONLD, 1)
	assert.Equal(t, "Senior Go Engineer", pc.JSONLD[0]["title"])

	require.NotNil(t, pc.NextData)
	props, _ := pc.NextData["props"].(map[string]any)
	require.NotNil(t, props)

	require.NotNil(t, pc.HTML)
	assert.Equal(t, "Senior Go Engineer", pc.HTML.Find("h1[itemprop=title]").Text())

	assert.Equal(t, "https://x.io/jobs/senior-go", pc.URL)
}

func TestNewPageContext_APIRecord(t *testing.T) {
	rec := map[string]any{"title": "From API"}
	pc, err := NewPageContext("https://x.io/api", "", rec)
	require.NoError(t, err)
	assert.Equal(t, "From API", pc.Record["title"])
}

func TestNewPageContext_ToleratesMalformedJSONLD(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{not json}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	assert.Empty(t, pc.JSONLD)
}

func TestNewPageContext_FlattensGraphWithoutWrapper(t *testing.T) {
	html := `<html><head><script type="application/ld+json">
{"@context":"https://schema.org","@graph":[{"@type":"Org","name":"ACME"},{"@type":"JobPosting","title":"Go Eng"}]}
</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	require.Len(t, pc.JSONLD, 2) // the two members only, not the wrapper
	assert.Equal(t, "ACME", pc.JSONLD[0]["name"])
	assert.Equal(t, "Go Eng", pc.JSONLD[1]["title"])
	for _, obj := range pc.JSONLD {
		_, hasGraph := obj["@graph"]
		assert.False(t, hasGraph, "wrapper object should not be present")
	}
}

func TestNewPageContext_MultipleJSONLDBlocks(t *testing.T) {
	html := `<html><head>
<script type="application/ld+json">{"@type":"A","title":"a"}</script>
<script type="application/ld+json">{"@type":"B","title":"b"}</script>
</head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	require.Len(t, pc.JSONLD, 2)
	assert.Equal(t, "a", pc.JSONLD[0]["title"])
	assert.Equal(t, "b", pc.JSONLD[1]["title"])
}

func TestNewPageContext_TopLevelJSONLDArray(t *testing.T) {
	html := `<html><head><script type="application/ld+json">[{"title":"a"},{"title":"b"}]</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	require.Len(t, pc.JSONLD, 2)
}

func TestNewPageContext_NextDataPrefersExactID(t *testing.T) {
	// A decoy application/json appears FIRST; the real state is in #__NEXT_DATA__.
	html := `<html><head>
<script type="application/json">{"decoy":true}</script>
<script id="__NEXT_DATA__" type="application/json">{"props":{"real":true}}</script>
</head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	require.NotNil(t, pc.NextData)
	props, _ := pc.NextData["props"].(map[string]any)
	require.NotNil(t, props)
	assert.Equal(t, true, props["real"])
}

func TestNewPageContext_NextDataFallsBackToFirstJSON(t *testing.T) {
	// No #__NEXT_DATA__: fall back to the first parseable application/json.
	html := `<html><head><script type="application/json">{"state":1}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	require.NotNil(t, pc.NextData)
	assert.EqualValues(t, 1, pc.NextData["state"])
}

func TestNewPageContext_MetaWithoutNameOrProperty(t *testing.T) {
	// charset-only / content-less meta tags must not panic and must be skipped.
	html := `<html><head><meta charset="utf-8"><meta name="x"></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	assert.Empty(t, pc.Meta)
}
