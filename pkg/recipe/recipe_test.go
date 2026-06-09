package recipe

import (
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
