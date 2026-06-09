package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRecipe_PassRate(t *testing.T) {
	reg := minimalRegistry(t)
	// A recipe that extracts the required fields from JSON-LD.
	jl := func(p string) FieldExtractor { return FieldExtractor{From: []string{"json_ld"}, JSONPath: p} }
	rec := &Recipe{Acquisition: "structured_data", Kind: KindRule{Mode: "source_default"},
		List: ListRule{Mode: "selector", ItemSelector: ".c", Link: FieldExtractor{From: []string{"selector"}, Selector: "a", Attr: "href"}, Pagination: Pagination{Mode: "none"}},
		Detail: DetailRule{RecordSource: "json_ld", Title: jl("$.title"), Description: jl("$.description"),
			IssuingEntity: jl("$.hiringOrganization.name"), ApplyURL: jl("$.url"), AnchorCountry: jl("$.jobLocation.address.addressCountry")}}

	good := SamplePage{URL: "https://x.io/1", HTML: `<html><head><script type="application/ld+json">{"title":"Go Eng","description":"A description that comfortably exceeds the fifty character verify minimum gate.","hiringOrganization":{"name":"ACME"},"url":"https://x.io/apply","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`}
	bad := SamplePage{URL: "https://x.io/2", HTML: `<html><body>no structured data here</body></html>`}

	src := genSource()
	rep := ValidateRecipe(rec, src, []SamplePage{good, bad}, reg)
	assert.Equal(t, 2, rep.Samples)
	assert.Equal(t, 1, rep.Passed)
	assert.InDelta(t, 0.5, rep.PassRate, 0.001)
	require.Len(t, rep.PerSample, 2)
	assert.True(t, rep.PerSample[0].OK)
	assert.False(t, rep.PerSample[1].OK)
}
