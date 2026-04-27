// Package deal is a stub matcher for the "deal" opportunity kind.
//
// The real implementation will match a candidate's interest categories
// against deal categories. The candidate-side input is a
// DealPreferences struct (preferred categories, max price, country).
//
// SearchFilter would filter Manticore to kind = "deal" plus AnyOf
// clauses on category and country.
//
// Score would combine:
//   - category-overlap scorer (Jaccard / weighted overlap between
//     candidate preferred categories and the deal's category list)
//   - freshness boost (deals expire fast — recency is a strong signal)
//   - price-fit (does the deal's price fall within the candidate's
//     stated max?)
//
// Until the real implementation lands the matcher returns a ScoreResult
// with Score=0 and Reasons=nil. Disabled() returns true so the
// onboarding UI hides this kind. The match router gates on Disabled(),
// but returning zero is the belt-and-braces guarantee that any leaked
// invocation still ranks the result to the bottom.
package deal

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher             { return &Matcher{} }
func (*Matcher) Kind() string   { return "deal" }
func (*Matcher) Disabled() bool { return true }

// SearchFilter still emits a kind-scoped Filter so that, when the real
// matcher lands and Disabled() flips, the kind-scope is correct from
// day one.
func (*Matcher) SearchFilter(prefs json.RawMessage) (any, error) {
	return searchindex.Filter{Kind: "deal"}, nil
}

func (*Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (matchers.ScoreResult, error) {
	return matchers.ScoreResult{Score: 0, Reasons: nil}, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
