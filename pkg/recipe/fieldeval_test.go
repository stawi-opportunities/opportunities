package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ctx(t *testing.T) *PageContext {
	t.Helper()
	pc, err := NewPageContext("https://x.io/jobs/senior-go", samplePage, nil)
	require.NoError(t, err)
	return pc
}

func TestEvaluate_JSONLDPath(t *testing.T) {
	fx := FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.title"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "Senior Go Engineer", got)
}

func TestEvaluate_NestedJSONLDPath(t *testing.T) {
	fx := FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.hiringOrganization.logo"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.x.io/acme.png", got)
}

func TestEvaluate_FromOrderingFirstNonEmptyWins(t *testing.T) {
	fx := FieldExtractor{
		From:     []string{"json_ld", "selector"},
		JSONPath: "$.missing",
		Selector: "h1[itemprop=title]",
	}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "Senior Go Engineer", got)
}

func TestEvaluate_SelectorAttrWithTransform(t *testing.T) {
	fx := FieldExtractor{From: []string{"selector"}, Selector: "a.apply", Attr: "href", Transform: []string{"absolute_url"}}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "https://x.io/apply/1", got)
}

func TestEvaluate_Meta(t *testing.T) {
	fx := FieldExtractor{From: []string{"meta"}, Meta: "og:image"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.x.io/logo.png", got)
}

func TestEvaluate_Const(t *testing.T) {
	fx := FieldExtractor{From: []string{"const"}, Const: "job"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "job", got)
}

func TestEvaluate_Record(t *testing.T) {
	pc, err := NewPageContext("https://x.io/api", "", map[string]any{"title": "API Title"})
	require.NoError(t, err)
	fx := FieldExtractor{From: []string{"record"}, JSONPath: "$.title"}
	got, err := Evaluate(fx, pc)
	require.NoError(t, err)
	assert.Equal(t, "API Title", got)
}

func TestEvaluate_EmptyWhenNothingResolves(t *testing.T) {
	fx := FieldExtractor{From: []string{"selector"}, Selector: ".nope"}
	got, err := Evaluate(fx, ctx(t))
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestEvaluateList_Selector(t *testing.T) {
	html := `<html><body><ul><li class="tag">Go</li><li class="tag">Remote</li></ul></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	fx := FieldExtractor{From: []string{"selector"}, Selector: "li.tag"}
	got, err := EvaluateList(fx, pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"Go", "Remote"}, got)
}

func TestEvaluateList_JSONLDArray(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"skills":["Go","SQL"]}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	fx := FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.skills"}
	got, err := EvaluateList(fx, pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"Go", "SQL"}, got)
}

func TestEvaluate_MicrodataContentAttr(t *testing.T) {
	html := `<html><body><span itemprop="addressCountry" content="KE">Kenya</span></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	got, err := Evaluate(FieldExtractor{From: []string{"microdata"}, Microdata: "addressCountry"}, pc)
	require.NoError(t, err)
	assert.Equal(t, "KE", got) // content attr wins over visible text
}

func TestEvaluate_MicrodataTextFallback(t *testing.T) {
	html := `<html><body><h1 itemprop="title">Hello</h1></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	got, err := Evaluate(FieldExtractor{From: []string{"microdata"}, Microdata: "title"}, pc)
	require.NoError(t, err)
	assert.Equal(t, "Hello", got)
}

func TestEvaluate_NextDataScalar(t *testing.T) {
	html := `<html><head><script id="__NEXT_DATA__" type="application/json">{"props":{"title":"From Next"}}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	got, err := Evaluate(FieldExtractor{From: []string{"next_data"}, JSONPath: "$.props.title"}, pc)
	require.NoError(t, err)
	assert.Equal(t, "From Next", got)
}

func TestEvaluate_FloatScalarNoTrailingZero(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"baseSalary":2026}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	got, err := Evaluate(FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.baseSalary"}, pc)
	require.NoError(t, err)
	assert.Equal(t, "2026", got) // not "2026.0"
}

func TestEvaluate_TransformErrorPropagates(t *testing.T) {
	html := `<html><head><script type="application/ld+json">{"deadline":"not a date"}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	_, err = Evaluate(FieldExtractor{From: []string{"json_ld"}, JSONPath: "$.deadline", Transform: []string{"parse_date"}}, pc)
	require.Error(t, err)
}

func TestEvaluateList_NextDataArray(t *testing.T) {
	html := `<html><head><script id="__NEXT_DATA__" type="application/json">{"tags":["a","b"]}</script></head><body></body></html>`
	pc, err := NewPageContext("https://x.io", html, nil)
	require.NoError(t, err)
	got, err := EvaluateList(FieldExtractor{From: []string{"next_data"}, JSONPath: "$.tags"}, pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestEvaluateList_UnsupportedSourceErrors(t *testing.T) {
	pc, err := NewPageContext("https://x.io", "<html><body></body></html>", nil)
	require.NoError(t, err)
	_, err = EvaluateList(FieldExtractor{From: []string{"const"}, Const: "x"}, pc)
	require.Error(t, err)
}
