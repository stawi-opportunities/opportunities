package recipe

import (
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// SampleResult is one sample's validation outcome.
type SampleResult struct {
	URL     string   `json:"url"`
	OK      bool     `json:"ok"`
	Missing []string `json:"missing,omitempty"`
	// Error carries a page-context or extraction failure. Without it a
	// build error reported as OK=false with NO missing fields — an
	// uninterpretable verdict for both operators and the repair loop.
	Error string `json:"error,omitempty"`
}

// ValidationReport summarizes a recipe dry-run over sample pages. It mirrors the
// shape stored in source_recipes.validation_report.
type ValidationReport struct {
	Samples   int            `json:"samples"`
	Passed    int            `json:"passed"`
	PassRate  float64        `json:"pass_rate"`
	PerSample []SampleResult `json:"per_sample,omitempty"`
}

// ValidateRecipe dry-runs the recipe's detail extraction over each sample page
// and runs opportunity.Verify on the result, producing a pass-rate. This is the
// quality gate: a recipe is accepted only when PassRate clears a threshold.
// (Detail-page samples cover structured_data/selectors recipes — the ones that
// need validation; api-mode recipes hit official APIs and are high-confidence.)
func ValidateRecipe(rec *Recipe, src domain.Source, samples []SamplePage, reg *opportunity.Registry) ValidationReport {
	rep := ValidationReport{Samples: len(samples)}
	for _, s := range samples {
		res := SampleResult{URL: s.URL}
		pc, err := NewPageContext(s.URL, s.HTML, nil)
		if err != nil {
			res.Error = err.Error()
		} else if opp, berr := buildOpportunity(pc, src, rec); berr != nil {
			res.Error = berr.Error()
		} else {
			v := opportunity.Verify(&opp, &src, reg)
			res.OK = v.OK
			res.Missing = v.Missing
		}
		if res.OK {
			rep.Passed++
		}
		rep.PerSample = append(rep.PerSample, res)
	}
	if rep.Samples > 0 {
		rep.PassRate = float64(rep.Passed) / float64(rep.Samples)
	}
	return rep
}
