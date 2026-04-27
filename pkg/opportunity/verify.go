package opportunity

import (
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// VerifyResult is the outcome of Verify. OK is true when the record satisfies
// the source contract and the kind's universal+kind-required attributes.
// Extra is informational — keys outside KindRequired ∪ KindOptional are
// surfaced but do not fail the record.
type VerifyResult struct {
	OK       bool
	Missing  []string // required-but-empty fields/attributes
	Extra    []string // attributes outside KindRequired ∪ KindOptional
	Mismatch string   // non-empty if Source did not declare this Kind
}

// Verify runs the full contract check between an extracted record and the
// source that produced it. The order of checks matters: source contract
// (Mismatch) is reported first so callers can route mismatches to a
// distinct dead-letter reason.
func Verify(opp *domain.ExternalOpportunity, src *domain.Source, reg *Registry) VerifyResult {
	res := VerifyResult{OK: true}

	// 1. Source contract
	if !inList(src.Kinds, opp.Kind) {
		res.OK = false
		res.Mismatch = fmt.Sprintf("kind %q not declared by source (declared: %v)", opp.Kind, src.Kinds)
		return res
	}

	spec, ok := reg.Lookup(opp.Kind)
	if !ok {
		res.OK = false
		res.Mismatch = fmt.Sprintf("kind %q not in registry", opp.Kind)
		return res
	}

	// 2. Universal required
	for _, k := range spec.UniversalRequired {
		if !universalFieldPresent(opp, k) {
			res.OK = false
			res.Missing = append(res.Missing, k)
		}
	}

	// 3. Kind required
	for _, k := range spec.KindRequired {
		if !attrPresent(opp.Attributes, k) {
			res.OK = false
			res.Missing = append(res.Missing, k)
		}
	}

	// 4. Source override (tightens kind required)
	if extra, ok := src.RequiredAttributesByKind[opp.Kind]; ok {
		for _, k := range extra {
			if !attrPresent(opp.Attributes, k) {
				res.OK = false
				res.Missing = append(res.Missing, k)
			}
		}
	}

	// 5. Stray attributes (warning only; do not flip OK)
	known := map[string]struct{}{}
	for _, k := range spec.KindRequired {
		known[k] = struct{}{}
	}
	for _, k := range spec.KindOptional {
		known[k] = struct{}{}
	}
	for k := range opp.Attributes {
		if _, ok := known[k]; !ok {
			res.Extra = append(res.Extra, k)
		}
	}

	return res
}

func inList(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func universalFieldPresent(opp *domain.ExternalOpportunity, key string) bool {
	switch key {
	case "title":
		return opp.Title != ""
	case "description":
		return len(opp.Description) >= 50
	case "issuing_entity":
		return opp.IssuingEntity != ""
	case "apply_url":
		return opp.ApplyURL != ""
	case "deadline":
		return opp.Deadline != nil
	case "anchor_country":
		return opp.AnchorLocation != nil && opp.AnchorLocation.Country != ""
	case "anchor_region":
		return opp.AnchorLocation != nil && opp.AnchorLocation.Region != ""
	case "anchor_city":
		return opp.AnchorLocation != nil && opp.AnchorLocation.City != ""
	}
	return false
}

func attrPresent(attrs map[string]any, key string) bool {
	if attrs == nil {
		return false
	}
	v, ok := attrs[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case nil:
		return false
	}
	return true
}
