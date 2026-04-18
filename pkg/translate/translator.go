// Package translate fans a CanonicalJob snapshot out into multiple
// target-language variants and uploads each variant to R2. Translations are
// never persisted in the database — CanonicalJob only carries pointers
// (translated_at, translated_langs) so a backfill can rediscover state from
// Postgres without dumping R2.
package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
)

// DefaultLanguages is the canonical 8-language set we translate to. The
// source language is stripped from this list at translate time so we never
// round-trip through the LLM.
var DefaultLanguages = []string{"en", "es", "fr", "de", "pt", "ja", "ar", "zh"}

// DisplayName returns the autonym (endonym) for an ISO 639-1 code — what a
// native speaker calls their own language. Used in the UI switcher.
var DisplayName = map[string]string{
	"en": "English",
	"es": "Español",
	"fr": "Français",
	"de": "Deutsch",
	"pt": "Português",
	"ja": "日本語",
	"ar": "العربية",
	"zh": "中文",
}

// Translator wraps an LLM extractor and turns JobSnapshot-shaped payloads
// into translated variants. It is a thin adapter; callers orchestrate the
// R2 fan-out.
type Translator struct {
	llm *extraction.Extractor
}

// New returns a Translator that calls the given extractor. If ext is nil,
// Translate will always return an error — the handler uses that to skip
// fan-out cleanly.
func New(ext *extraction.Extractor) *Translator {
	return &Translator{llm: ext}
}

// TranslatableFields is the subset of JobSnapshot that carries
// user-readable text. Everything else (IDs, slugs, URLs, numbers, dates)
// is copied verbatim to preserve structural identity across languages.
type TranslatableFields struct {
	Title           string   `json:"title"`
	DescriptionHTML string   `json:"description_html"`
	LocationText    string   `json:"location_text,omitempty"`
	Required        []string `json:"required_skills"`
	NiceToHave      []string `json:"nice_to_have_skills"`
}

// Translate returns a JobSnapshot whose user-facing fields are rendered in
// targetLang. sourceLang is the ISO 639-1 code from the canonical job (used
// only to prompt the LLM, not to gate the call).
func (t *Translator) Translate(ctx context.Context, snap domain.JobSnapshot, sourceLang, targetLang string) (domain.JobSnapshot, error) {
	if t.llm == nil {
		return snap, errors.New("translator: no LLM extractor configured")
	}
	src := strings.ToLower(strings.TrimSpace(sourceLang))
	tgt := strings.ToLower(strings.TrimSpace(targetLang))
	if tgt == "" {
		return snap, errors.New("translator: empty target language")
	}
	if src == tgt {
		return snap, nil
	}

	in := TranslatableFields{
		Title:           snap.Title,
		DescriptionHTML: snap.DescriptionHTML,
		LocationText:    snap.Location.Text,
		Required:        snap.Skills.Required,
		NiceToHave:      snap.Skills.NiceToHave,
	}
	inJSON, err := json.Marshal(in)
	if err != nil {
		return snap, fmt.Errorf("marshal input: %w", err)
	}

	system := buildSystemPrompt(src, tgt)
	raw, err := t.llm.Prompt(ctx, system, string(inJSON))
	if err != nil {
		return snap, fmt.Errorf("translate %s→%s: %w", src, tgt, err)
	}

	var out TranslatableFields
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return snap, fmt.Errorf("translate %s→%s: unmarshal response: %w", src, tgt, err)
	}

	result := snap
	result.Language = tgt
	if out.Title != "" {
		result.Title = out.Title
	}
	if out.DescriptionHTML != "" {
		result.DescriptionHTML = out.DescriptionHTML
	}
	if out.LocationText != "" {
		result.Location.Text = out.LocationText
	}
	if len(out.Required) > 0 {
		result.Skills.Required = out.Required
	}
	if len(out.NiceToHave) > 0 {
		result.Skills.NiceToHave = out.NiceToHave
	}
	return result, nil
}

// buildSystemPrompt produces the translator's system message. It asks for
// strict JSON with the same shape as TranslatableFields so we can unmarshal
// the response directly. HTML tags must survive untouched because the shell
// re-sanitises them anyway; rewriting them would just burn tokens on
// markup the bluemonday whitelist already enforces.
func buildSystemPrompt(source, target string) string {
	return fmt.Sprintf(`You are a professional translator specializing in job postings. Translate the user-provided JSON object from %s to %s.

Rules:
1. Return a JSON object with EXACTLY these keys: title, description_html, location_text, required_skills, nice_to_have_skills.
2. Preserve every HTML tag in description_html verbatim; translate only the text between tags.
3. Keep company names, product names, and industry acronyms untouched (e.g. "React", "Kubernetes", "SaaS").
4. required_skills and nice_to_have_skills are arrays; translate each entry but keep tool/framework names untouched.
5. For location_text, translate city/country names if they have a natural %s equivalent, otherwise leave as-is.
6. Do NOT add commentary, prefixes, or markdown code fences. Output raw JSON.`, languageName(source), languageName(target), languageName(target))
}

// languageName maps an ISO 639-1 code to a human-readable English name for
// use in the system prompt. Unknown codes fall back to the code itself.
func languageName(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "en":
		return "English"
	case "es":
		return "Spanish"
	case "fr":
		return "French"
	case "de":
		return "German"
	case "pt":
		return "Portuguese"
	case "ja":
		return "Japanese"
	case "ar":
		return "Arabic"
	case "zh":
		return "Chinese (Simplified)"
	case "ko":
		return "Korean"
	case "hi":
		return "Hindi"
	case "ru":
		return "Russian"
	case "it":
		return "Italian"
	default:
		return code
	}
}

// Normalize trims whitespace, lowercases, dedupes, and drops empties.
// Exposed so handlers can sanitize env-supplied lists.
func Normalize(codes []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}
