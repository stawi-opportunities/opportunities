package recipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/PaesslerAG/jsonpath"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Executor runs a recipe deterministically — no LLM. It is constructed per
// source with that source's active recipe and a Fetcher for HTTP.
type Executor struct {
	recipe  *Recipe
	fetcher Fetcher
}

// NewExecutor builds an Executor for a recipe + fetcher.
func NewExecutor(r *Recipe, f Fetcher) *Executor {
	return &Executor{recipe: r, fetcher: f}
}

// apiPage fetches one api-mode page from pageURL, parses the records under
// List.ItemsPath, and builds an opportunity per record. It returns the page's
// items, the raw body, the HTTP status, the root JSON (for cursor pagination),
// and any error.
func (e *Executor) apiPage(ctx context.Context, src domain.Source, pageURL string) (items []domain.ExternalOpportunity, raw []byte, status int, root any, err error) {
	raw, status, err = e.fetcher.Get(ctx, pageURL)
	if err != nil {
		return nil, raw, status, nil, err
	}
	if status < 200 || status >= 300 {
		return nil, raw, status, nil, fmt.Errorf("api page %s returned status %d", pageURL, status)
	}
	if err = json.Unmarshal(raw, &root); err != nil {
		return nil, raw, status, nil, fmt.Errorf("api page %s: invalid JSON: %w", pageURL, err)
	}
	recsVal, err := jsonpath.Get(e.recipe.List.ItemsPath, root)
	if err != nil {
		return nil, raw, status, root, fmt.Errorf("api page %s: items_path %q: %w", pageURL, e.recipe.List.ItemsPath, err)
	}
	recs, ok := recsVal.([]any)
	if !ok {
		return nil, raw, status, root, nil
	}
	for _, rv := range recs {
		rec, ok := rv.(map[string]any)
		if !ok {
			continue
		}
		pc, perr := NewPageContext(pageURL, "", rec)
		if perr != nil {
			return nil, raw, status, root, perr
		}
		opp, berr := buildOpportunity(pc, src, e.recipe)
		if berr != nil {
			return nil, raw, status, root, berr
		}
		items = append(items, opp)
	}
	return items, raw, status, root, nil
}
