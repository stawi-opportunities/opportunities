// Package recipecheck is the single source of truth for answering "does
// this recipe crawl this source correctly?". It runs the same code paths
// production uses — structural validation, detail extraction +
// opportunity.Verify, the list-rule probe, and a full Executor page —
// and returns a structured report. The recipe-verify CLI, the fixture
// tests under tests/recipes, and ad-hoc operator checks all call this
// one function so a recipe can never be "verified" by a weaker check.
package recipecheck

import (
	"context"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// Report is the structured outcome of one source-against-recipe check.
type Report struct {
	StructuralOK  bool                    `json:"structural_ok"`
	StructuralErr string                  `json:"structural_err,omitempty"`
	Detail        recipe.ValidationReport `json:"detail"`
	ListItems     int                     `json:"list_items"`
	ListErr       string                  `json:"list_err,omitempty"`
	PageItems     int                     `json:"page_items"`
	PageVerified  int                     `json:"page_verified"`
	PageErr       string                  `json:"page_err,omitempty"`
	MorePages     bool                    `json:"more_pages"`
}

// OK is the overall verdict: the recipe parses, extracts the required
// fields from the sample detail pages at or above threshold, its list
// rule finds items, and a real page execution yields at least one item
// that passes opportunity.Verify end-to-end.
func (r Report) OK(threshold float64) bool {
	return r.StructuralOK &&
		r.Detail.PassRate >= threshold &&
		r.ListItems > 0 &&
		r.PageItems > 0 && r.PageVerified > 0
}

// Summary renders a one-line verdict for logs and test failures.
func (r Report) Summary() string {
	return fmt.Sprintf(
		"structural_ok=%v detail_pass=%.2f list_items=%d page_items=%d page_verified=%d list_err=%q page_err=%q",
		r.StructuralOK, r.Detail.PassRate, r.ListItems, r.PageItems, r.PageVerified, r.ListErr, r.PageErr)
}

// Check verifies rec against src using fetcher for every page access.
// samples are detail pages for the extraction gate (already fetched —
// callers decide whether they come from live discovery or recorded
// fixtures). The full-page step fetches the listing and its detail
// pages through fetcher, so a fixture-backed fetcher makes the whole
// check hermetic.
func Check(ctx context.Context, fetcher recipe.Fetcher, reg *opportunity.Registry,
	src domain.Source, rec *recipe.Recipe, samples []recipe.SamplePage) Report {

	var rep Report

	rec.Normalize()
	if err := rec.Validate(); err != nil {
		rep.StructuralErr = err.Error()
		return rep
	}
	rep.StructuralOK = true

	rep.Detail = recipe.ValidateRecipe(rec, src, samples, reg)

	ex := recipe.NewExecutor(rec, fetcher)
	n, err := ex.ListProbe(ctx, src)
	rep.ListItems = n
	if err != nil {
		rep.ListErr = err.Error()
	}

	items, _, _, _, done, err := ex.Page(ctx, src, recipe.PageState{})
	rep.PageItems = len(items)
	rep.MorePages = !done
	if err != nil {
		rep.PageErr = err.Error()
		return rep
	}
	for i := range items {
		if v := opportunity.Verify(&items[i], &src, reg); v.OK {
			rep.PageVerified++
		}
	}
	return rep
}
