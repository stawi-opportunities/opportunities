// Package opportunity defines the kind registry that drives polymorphic
// behaviour across the platform: extraction prompts, verification rules,
// search facets, URL prefixes, onboarding flows, and matchers.
package opportunity

import (
	"fmt"
	"regexp"
)

// Spec is the contract a Kind imposes on the pipeline. It is the single
// source of truth: extraction reads ExtractionPrompt, verify reads
// UniversalRequired/KindRequired, search reads SearchFacets, the UI reads
// OnboardingFlow, the matcher router reads Matcher. Adding a kind means
// authoring one Spec — usually as YAML under definitions/opportunity-kinds/.
type Spec struct {
	Kind               string   `yaml:"kind"`
	DisplayName        string   `yaml:"display_name"`
	IssuingEntityLabel string   `yaml:"issuing_entity_label"`
	AmountKind         string   `yaml:"amount_kind"`
	URLPrefix          string   `yaml:"url_prefix"`
	UniversalRequired  []string `yaml:"universal_required"`
	KindRequired       []string `yaml:"kind_required"`
	KindOptional       []string `yaml:"kind_optional"`
	Categories         []string `yaml:"categories"`
	SearchFacets       []string `yaml:"search_facets"`
	ExtractionPrompt   string   `yaml:"extraction_prompt"`
	OnboardingFlow     string   `yaml:"onboarding_flow"`
	Matcher            string   `yaml:"matcher"`
}

var urlPrefixRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// universalKeys is the closed set of envelope fields that may appear in
// UniversalRequired. Anything else is a YAML authoring error.
var universalKeys = map[string]struct{}{
	"title":          {},
	"description":    {},
	"issuing_entity": {},
	"apply_url":      {},
	"anchor_country": {},
	"anchor_region":  {},
	"anchor_city":    {},
}

func (s Spec) Validate() error {
	if s.Kind == "" {
		return fmt.Errorf("spec: kind is required")
	}
	if s.DisplayName == "" {
		return fmt.Errorf("spec %q: display_name is required", s.Kind)
	}
	if s.IssuingEntityLabel == "" {
		return fmt.Errorf("spec %q: issuing_entity_label is required", s.Kind)
	}
	if !urlPrefixRE.MatchString(s.URLPrefix) {
		return fmt.Errorf("spec %q: url_prefix %q must match %s", s.Kind, s.URLPrefix, urlPrefixRE)
	}
	for _, k := range s.UniversalRequired {
		if _, ok := universalKeys[k]; !ok {
			return fmt.Errorf("spec %q: universal_required[%q] is not a known envelope key", s.Kind, k)
		}
	}
	return nil
}
