package recipe

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validRecipe() *Recipe {
	req := func(from, path string) FieldExtractor {
		return FieldExtractor{From: []string{from}, JSONPath: path, Required: true}
	}
	return &Recipe{
		Version:     1,
		Acquisition: "structured_data",
		Kind:        KindRule{Mode: "source_default"},
		List: ListRule{
			Mode:         "selector",
			ItemSelector: ".job-card",
			Link:         FieldExtractor{From: []string{"selector"}, Selector: "a", Attr: "href", Transform: []string{"absolute_url"}},
			Pagination:   Pagination{Mode: "none"},
		},
		Detail: DetailRule{
			RecordSource:  "json_ld",
			Title:         req("json_ld", "$.title"),
			Description:   req("json_ld", "$.description"),
			IssuingEntity: req("json_ld", "$.hiringOrganization.name"),
			ApplyURL:      req("json_ld", "$.url"),
			AnchorCountry: req("json_ld", "$.jobLocation.address.addressCountry"),
		},
	}
}

func TestValidate_AcceptsValidRecipe(t *testing.T) {
	require.NoError(t, validRecipe().Validate())
}

func TestValidate_RejectsBadAcquisition(t *testing.T) {
	r := validRecipe()
	r.Acquisition = "telepathy"
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acquisition")
}

func TestValidate_RejectsMissingRequiredEnvelopeField(t *testing.T) {
	r := validRecipe()
	r.Detail.Title = FieldExtractor{}
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "title")
}

func TestValidate_RejectsUnknownTransform(t *testing.T) {
	r := validRecipe()
	r.Detail.Title.Transform = []string{"frobnicate"}
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frobnicate")
}

func TestValidate_RejectsUnparseableSelector(t *testing.T) {
	r := validRecipe()
	r.Detail.Title = FieldExtractor{From: []string{"selector"}, Selector: "a[unclosed"}
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selector")
}

func TestValidate_RejectsUnknownFromSource(t *testing.T) {
	r := validRecipe()
	r.Detail.Title = FieldExtractor{From: []string{"ouija"}, Const: "x"}
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ouija")
}

func TestRecipeJSON_OmitsEmptyExtractors(t *testing.T) {
	b, err := json.Marshal(validRecipe())
	require.NoError(t, err)
	s := string(b)
	// Optional empty extractors must not serialize as empty objects.
	assert.NotContains(t, s, `"location_text":{}`)
	assert.NotContains(t, s, `"cursor":{}`)
	assert.NotContains(t, s, `"company_logo_url":{}`)
}

func TestTransformList_TolerantUnmarshal(t *testing.T) {
	var fx FieldExtractor
	// array of strings (canonical)
	require.NoError(t, json.Unmarshal([]byte(`{"transform":["trim","lower"]}`), &fx))
	assert.Equal(t, TransformList{"trim", "lower"}, fx.Transform)
	// bare string
	require.NoError(t, json.Unmarshal([]byte(`{"transform":"trim"}`), &fx))
	assert.Equal(t, TransformList{"trim"}, fx.Transform)
	// object (the prod failure shape) — dropped, not fatal
	require.NoError(t, json.Unmarshal([]byte(`{"transform":{"contains":"Remote"}}`), &fx))
	assert.Empty(t, fx.Transform)
	// mixed array — strings kept, junk dropped
	require.NoError(t, json.Unmarshal([]byte(`{"transform":["trim",{"x":1},5]}`), &fx))
	assert.Equal(t, TransformList{"trim"}, fx.Transform)
}

func TestFieldExtractor_TolerantUnmarshal(t *testing.T) {
	var p Pagination
	// the exact prod failure: bare string where an extractor object belongs
	require.NoError(t, json.Unmarshal([]byte(`{"mode":"next_link","next":"a.next"}`), &p))
	assert.Equal(t, []string{"selector"}, p.Next.From)
	assert.Equal(t, "a.next", p.Next.Selector)
	// canonical object form still works
	var fx FieldExtractor
	require.NoError(t, json.Unmarshal([]byte(`{"from":["json_ld"],"json_path":"$.title"}`), &fx))
	assert.Equal(t, []string{"json_ld"}, fx.From)
	// junk shape -> empty, not a parse failure
	require.NoError(t, json.Unmarshal([]byte(`{"next":42}`), &struct {
		Next FieldExtractor `json:"next"`
	}{}))
}

// TestNormalizeCoercesFromAliases: LLMs emit From-source aliases despite
// the prompt whitelisting canonical names; Normalize maps them instead of
// burning repair attempts on cosmetic drift.
func TestNormalizeCoercesFromAliases(t *testing.T) {
	r := Recipe{
		Acquisition: "structured_data",
		Kind:        KindRule{Mode: "source_default"},
		List:        ListRule{Mode: "selector", ItemSelector: ".c", Pagination: Pagination{Mode: "none"}},
		Detail: DetailRule{
			Title:         FieldExtractor{From: []string{"og:title"}},
			Description:   FieldExtractor{From: []string{"html"}, Selector: ".desc"},
			IssuingEntity: FieldExtractor{From: []string{"jsonld"}, JSONPath: "$.org"},
			ApplyURL:      FieldExtractor{From: []string{"url"}},
			AnchorCountry: FieldExtractor{From: []string{"bogus", "const"}, Const: "KE"},
		},
	}
	r.Normalize()
	if got := r.Detail.Title.From[0]; got != "meta" || r.Detail.Title.Meta != "og:title" {
		t.Fatalf("og:title coercion: from=%v meta=%q", r.Detail.Title.From, r.Detail.Title.Meta)
	}
	if got := r.Detail.Description.From[0]; got != "selector" {
		t.Fatalf("html coercion: %v", r.Detail.Description.From)
	}
	if got := r.Detail.IssuingEntity.From[0]; got != "json_ld" {
		t.Fatalf("jsonld coercion: %v", r.Detail.IssuingEntity.From)
	}
	if got := r.Detail.ApplyURL.From[0]; got != "page_url" {
		t.Fatalf("url coercion: %v", r.Detail.ApplyURL.From)
	}
	if got := r.Detail.AnchorCountry.From; len(got) != 1 || got[0] != "const" {
		t.Fatalf("bogus source must be dropped, valid kept: %v", got)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("normalized recipe must validate: %v", err)
	}
}
