// Package tender is a stub matcher for the "tender" opportunity kind.
//
// The real implementation will match a candidate's company profile against
// tender procurement requirements. The candidate-side input is a
// TenderPreferences struct with fields:
//
//	{
//	  company_name,
//	  registration_country,
//	  capabilities,
//	  certifications,
//	  budget_capacity_range,
//	  service_capability_regions,
//	}
//
// SearchFilter would filter Manticore to:
//   - kind = "tender"
//   - country IN <service_capability_regions>
//   - procurement_domain IN <capabilities>
//
// Score would combine:
//   - capability overlap (Jaccard between candidate capabilities and the
//     tender's required procurement_domains)
//   - geographic match (binary: candidate's regions cover the tender's
//     anchor country)
//   - budget fit (does the tender's amount range fall within the
//     candidate's stated budget_capacity_range?)
//
// Until the real implementation lands the matcher returns a ScoreResult
// with Score=0 and Reasons=nil. Disabled() returns true so the
// onboarding UI hides this kind. The match router gates on Disabled(),
// but returning zero is the belt-and-braces guarantee that any leaked
// invocation still ranks the result to the bottom.
package tender

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher             { return &Matcher{} }
func (*Matcher) Kind() string   { return "tender" }
func (*Matcher) Disabled() bool { return true }

// SearchFilter still emits a kind-scoped Filter so that, when the real
// matcher lands and Disabled() flips, the kind-scope is correct from
// day one.
func (*Matcher) SearchFilter(prefs json.RawMessage) (any, error) {
	return searchindex.Filter{Kind: "tender"}, nil
}

func (*Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (matchers.ScoreResult, error) {
	return matchers.ScoreResult{Score: 0, Reasons: nil}, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
