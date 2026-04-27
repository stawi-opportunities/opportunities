// Package funding is a stub matcher for the "funding" opportunity kind.
//
// The real implementation will match an organisation profile against
// funding opportunity eligibility. The candidate-side input is a
// FundingPreferences struct with fields:
//
//	{
//	  organisation_type,   // "nonprofit" | "for_profit" | "individual"
//	  focus_areas,
//	  country,
//	  funding_amount_target,
//	}
//
// SearchFilter would filter Manticore to:
//   - kind = "funding"
//   - organisation_eligibility CONTAINS <organisation_type>
//   - focus_area overlap with candidate focus_areas
//   - target_regions CONTAINS <country>
//
// Score would combine:
//   - funding-amount fit (does the funding range overlap the
//     candidate's funding_amount_target?)
//   - focus alignment (Jaccard between candidate focus_areas and the
//     opportunity's focus_area list)
//   - deadline horizon (further-out deadlines score higher because the
//     org has more runway to apply)
//
// Until the real implementation lands the matcher returns a ScoreResult
// with Score=0 and Reasons=nil. Disabled() returns true so the
// onboarding UI hides this kind. The match router gates on Disabled(),
// but returning zero is the belt-and-braces guarantee that any leaked
// invocation still ranks the result to the bottom.
package funding

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher             { return &Matcher{} }
func (*Matcher) Kind() string   { return "funding" }
func (*Matcher) Disabled() bool { return true }

// SearchFilter still emits a kind-scoped Filter so that, when the real
// matcher lands and Disabled() flips, the kind-scope is correct from
// day one.
func (*Matcher) SearchFilter(prefs json.RawMessage) (any, error) {
	return searchindex.Filter{Kind: "funding"}, nil
}

func (*Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (matchers.ScoreResult, error) {
	return matchers.ScoreResult{Score: 0, Reasons: nil}, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
