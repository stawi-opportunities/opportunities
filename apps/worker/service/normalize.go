package service

import (
	"strings"
	"time"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type Normalized struct {
	VariantID, SourceID, HardKey, Kind, ApplyURL string
	NormalizedAt                                 time.Time
	Attributes                                   map[string]any
}

// Normalize performs deterministic cleanup without network or broker I/O.
func Normalize(in eventsv1.VariantIngestedV1) Normalized {
	attrs := make(map[string]any, len(in.Attributes)+6)
	for k, v := range in.Attributes {
		attrs[k] = v
	}
	trim := func(key string) string {
		s, _ := attrs[key].(string)
		s = strings.TrimSpace(s)
		attrs[key] = s
		return s
	}
	trim("location_text")
	attrs["language"] = strings.ToLower(trim("language"))
	attrs["employment_type"] = strings.ToLower(trim("employment_type"))
	rt := strings.ToLower(trim("remote_type"))
	if rt == "" {
		loc, _ := attrs["location_text"].(string)
		switch {
		case strings.Contains(strings.ToLower(loc), "remote"), strings.Contains(strings.ToLower(loc), "anywhere"), strings.Contains(strings.ToLower(loc), "worldwide"):
			rt = "remote"
		case strings.Contains(strings.ToLower(loc), "hybrid"):
			rt = "hybrid"
		}
	}
	attrs["remote_type"] = rt
	trim("description")
	attrs["title"] = strings.TrimSpace(in.Title)
	attrs["issuing_entity"] = strings.TrimSpace(in.IssuingEntity)
	attrs["country"] = strings.ToUpper(strings.TrimSpace(in.AnchorCountry))
	attrs["currency"] = strings.ToUpper(strings.TrimSpace(in.Currency))
	attrs["amount_min"] = in.AmountMin
	attrs["amount_max"] = in.AmountMax
	return Normalized{VariantID: in.VariantID, SourceID: in.SourceID, HardKey: in.HardKey, Kind: in.Kind, ApplyURL: strings.TrimSpace(in.ApplyURL), NormalizedAt: time.Now().UTC(), Attributes: attrs}
}
