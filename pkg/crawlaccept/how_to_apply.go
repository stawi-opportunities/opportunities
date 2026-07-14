package crawlaccept

import (
	"context"
	"strings"
	"unicode/utf8"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// HowToApplyPeeler peels application instructions from a public description
// using inference (or a test double). Implemented by *extraction.Extractor.
type HowToApplyPeeler interface {
	PeelHowToApply(ctx context.Context, title, kind, description string) (cleanDescription, howToApply string, err error)
}

// minPeelRunes mirrors extraction.minHowToApplyRunes — avoid burning an
// inference call on tiny stubs.
const minPeelRunes = 80

// PeelAccepted mutates an accepted ingest payload so paywalled application
// instructions ride on HowToApply and Attributes["description"] holds only
// public details.
//
// No-op when:
//   - peeler is nil
//   - HowToApply is already set (connector / kind extraction provided it)
//   - description is missing or too short
//   - the model returns empty how_to_apply
//
// Fail-open: inference errors leave the payload unchanged so crawl never
// blocks on a transient LLM outage.
func PeelAccepted(ctx context.Context, p *eventsv1.VariantIngestedV1, peeler HowToApplyPeeler) error {
	if p == nil || peeler == nil {
		return nil
	}
	if strings.TrimSpace(p.HowToApply) != "" {
		return nil
	}
	desc, _ := p.Attributes["description"].(string)
	desc = strings.TrimSpace(desc)
	if desc == "" || utf8.RuneCountInString(desc) < minPeelRunes {
		return nil
	}
	clean, how, err := peeler.PeelHowToApply(ctx, p.Title, p.Kind, desc)
	if err != nil {
		return err
	}
	how = strings.TrimSpace(how)
	if how == "" {
		return nil
	}
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return nil
	}
	if p.Attributes == nil {
		p.Attributes = map[string]any{}
	}
	p.Attributes["description"] = clean
	delete(p.Attributes, "how_to_apply")
	p.HowToApply = how
	return nil
}
