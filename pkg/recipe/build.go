package recipe

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// buildOpportunity maps a PageContext through the recipe's DetailRule into a
// domain.ExternalOpportunity. It is kind-agnostic: every kind-specific field
// flows through DetailRule.Attributes. Transform errors on a field are treated
// as "field absent" — the downstream opportunity.Verify() gate decides whether
// a missing required field is fatal.
func buildOpportunity(pc *PageContext, src domain.Source, r *Recipe) (domain.ExternalOpportunity, error) {
	kind, err := resolveKind(r, pc, src)
	if err != nil {
		return domain.ExternalOpportunity{}, err
	}

	val := func(fx FieldExtractor) string {
		if fx.empty() {
			return ""
		}
		v, _ := Evaluate(fx, pc)
		return v
	}

	d := r.Detail
	opp := domain.ExternalOpportunity{
		Kind:          kind,
		SourceID:      src.ID,
		Source:        src.Type,
		SourceURL:     pc.URL,
		Title:         val(d.Title),
		Description:   val(d.Description),
		IssuingEntity: val(d.IssuingEntity),
		ApplyURL:      val(d.ApplyURL),
		LocationText:  val(d.LocationText),
		Remote:        parseBool(val(d.Remote)),
		PostedAt:      parseTime(val(d.PostedAt)),
		Deadline:      parseTime(val(d.Deadline)),
		AmountMin:     parseFloat(val(d.AmountMin)),
		AmountMax:     parseFloat(val(d.AmountMax)),
		Currency:      val(d.Currency),
	}
	// A detail page is itself an actionable destination when the source does
	// not expose a separate application link. Keep this first-class so every
	// parser result can support application flows without another lookup.
	if strings.TrimSpace(opp.ApplyURL) == "" {
		opp.ApplyURL = pc.URL
	}

	opp.ExternalID = opp.ApplyURL

	country := val(d.AnchorCountry)
	if country == "" {
		country = src.Country
	}
	if country != "" {
		opp.AnchorLocation = &domain.Location{Country: country}
	}

	if !d.Categories.empty() {
		if cats, _ := EvaluateList(d.Categories, pc); len(cats) > 0 {
			opp.Categories = cats
		}
	}

	attrs := map[string]any{}
	for key, fx := range d.Attributes {
		if v := val(fx); v != "" {
			attrs[key] = v
		}
	}
	if v := val(d.CompanyLogoURL); v != "" {
		attrs["company_logo_url"] = v
	}
	if v := val(d.CompanyProfile); v != "" {
		attrs["company_profile"] = v
	}
	if len(attrs) > 0 {
		opp.Attributes = attrs
	}

	return opp, nil
}

// resolveKind determines the opportunity kind deterministically — never a
// per-page LLM classifier.
func resolveKind(r *Recipe, pc *PageContext, src domain.Source) (string, error) {
	switch r.Kind.Mode {
	case "fixed":
		return r.Kind.Fixed, nil
	case "by_path":
		v, _ := Evaluate(r.Kind.Path, pc)
		return v, nil
	default: // "source_default"
		kinds := []string(src.Kinds)
		switch len(kinds) {
		case 1:
			return kinds[0], nil
		case 0:
			return "", nil
		default:
			return "", fmt.Errorf("kind source_default needs exactly one source kind, source has %d; recipe must use fixed or by_path", len(kinds))
		}
	}
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "remote", "1":
		return true
	}
	return false
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
