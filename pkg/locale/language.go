package locale

import (
	"sort"
	"strconv"
	"strings"
)

// BaseTag collapses a BCP-47 language tag to its base subtag.
// Examples:
//
//	"en-US"    → "en"
//	"sw-KE"    → "sw"
//	"zh-Hans"  → "zh"
//	"pt_BR"    → "pt"
//	""         → ""
//
// Job postings get stored with a base tag already (see the ingestion
// normalise step), so base-only comparison keeps the match table small
// and matches how most source sites tag content.
func BaseTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	// BCP-47 separator is "-", but clients occasionally send "_".
	for _, sep := range []string{"-", "_"} {
		if i := strings.Index(tag, sep); i > 0 {
			return strings.ToLower(tag[:i])
		}
	}
	return strings.ToLower(tag)
}

// LanguageMatches returns true when the job's language tag matches
// the user's language tag at the base-subtag level. Both inputs may
// be empty; empty-vs-anything returns false (we don't guess).
func LanguageMatches(jobTag, userTag string) bool {
	j := BaseTag(jobTag)
	u := BaseTag(userTag)
	if j == "" || u == "" {
		return false
	}
	return j == u
}

// ParseAcceptLanguage extracts base language tags from an
// Accept-Language header (or navigator.languages joined with commas),
// ordered by q-weight descending. Tags past position 5 are dropped —
// a user with 8 declared languages is not realistically filtered by
// the 8th. Duplicates after base-tag collapse are removed.
//
//	"en-US,en;q=0.9,sw-KE;q=0.8,fr;q=0.5" → ["en", "sw", "fr"]
func ParseAcceptLanguage(header string) []string {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	type weighted struct {
		base string
		q    float64
		pos  int // stable-sort tie-breaker: earlier position wins
	}
	const maxLangs = 5

	seen := make(map[string]int, 4) // base → index in langs
	var langs []weighted

	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Split off q-weight: "en;q=0.9" → tag="en", q=0.9
		tag := part
		q := 1.0
		if semi := strings.Index(part, ";"); semi >= 0 {
			tag = strings.TrimSpace(part[:semi])
			rest := part[semi+1:]
			if idx := strings.Index(rest, "q="); idx >= 0 {
				if v := strings.TrimSpace(rest[idx+2:]); v != "" {
					if parsed, err := parseQ(v); err == nil {
						q = parsed
					}
				}
			}
		}
		base := BaseTag(tag)
		if base == "" {
			continue
		}
		if existing, ok := seen[base]; ok {
			// Keep the higher-q occurrence; on ties, earlier wins.
			if q > langs[existing].q {
				langs[existing].q = q
				langs[existing].pos = i
			}
			continue
		}
		seen[base] = len(langs)
		langs = append(langs, weighted{base: base, q: q, pos: i})
		if len(langs) >= maxLangs*2 {
			break // bound the work; we'll trim below anyway
		}
	}

	sort.SliceStable(langs, func(i, j int) bool {
		if langs[i].q != langs[j].q {
			return langs[i].q > langs[j].q
		}
		return langs[i].pos < langs[j].pos
	})
	if len(langs) > maxLangs {
		langs = langs[:maxLangs]
	}
	out := make([]string, len(langs))
	for i, l := range langs {
		out[i] = l.base
	}
	return out
}

// parseQ parses a q-weight string (e.g. "0.9", "1.0"). Returns q in
// [0, 1]; caller uses it only as a sort key, so tolerable precision
// is 3 decimals.
func parseQ(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
