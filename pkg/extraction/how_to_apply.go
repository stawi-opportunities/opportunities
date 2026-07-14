package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// minHowToApplyRunes skips inference when the description is too short to
// plausibly contain a separate application-instructions section.
const minHowToApplyRunes = 80

// maxHowToApplyInputRunes caps the document sent to the model. Apply
// instructions usually sit near the end, so when we must truncate we keep
// the head (role details) and the tail (where "how to apply" typically lives).
const maxHowToApplyInputRunes = 6000

// HowToApplySplit is the model output for peeling paywalled application
// instructions out of a public opportunity description.
type HowToApplySplit struct {
	// Description is the public body with application instructions removed.
	Description string `json:"description"`
	// HowToApply holds application steps, contacts, emails, portal notes.
	// Empty when the source has no separate instructions beyond apply_url.
	HowToApply string `json:"how_to_apply"`
}

// PeelHowToApply uses inference to separate public role/details Markdown from
// paywalled application instructions. It is fail-open for callers: on model
// or parse failure the original description is returned with empty howToApply.
//
// Prefer this over heading regexes — listing copy is multi-language and
// inconsistently structured; the model is the contract for "what is apply
// guidance vs. role description".
func (e *Extractor) PeelHowToApply(ctx context.Context, title, kind, description string) (cleanDescription, howToApply string, err error) {
	desc := strings.TrimSpace(description)
	if desc == "" {
		return "", "", nil
	}
	if utf8.RuneCountInString(desc) < minHowToApplyRunes {
		return desc, "", nil
	}
	if e == nil || e.llm == nil {
		return desc, "", fmt.Errorf("extraction: peel how_to_apply: no LLM configured")
	}

	doc := balanceTruncate(desc, maxHowToApplyInputRunes)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "job"
	}
	title = strings.TrimSpace(title)

	prompt := howToApplyPeelPrompt + "\n\n" +
		"kind: " + kind + "\n" +
		"title: " + title + "\n\n" +
		"Document (Markdown):\n" + doc

	raw, err := e.llm.Complete(ctx, prompt)
	if err != nil {
		return desc, "", fmt.Errorf("extraction: peel how_to_apply: %w", err)
	}
	split, err := parseHowToApplySplit(raw)
	if err != nil {
		return desc, "", fmt.Errorf("extraction: peel how_to_apply parse: %w", err)
	}

	clean := strings.TrimSpace(split.Description)
	how := strings.TrimSpace(split.HowToApply)
	if clean == "" {
		// Model dropped the body — refuse to blank the public description.
		return desc, how, nil
	}
	// If the model returned an identical body and non-empty how_to_apply,
	// still accept how_to_apply (details may legitimately stay long).
	return clean, how, nil
}

const howToApplyPeelPrompt = `You separate opportunity listing Markdown into two parts for a job board paywall.

Return ONLY a JSON object with exactly these keys:
  "description"  — public role / opportunity details (Markdown)
  "how_to_apply" — application instructions only (Markdown), or omit / "" if none

Rules for "how_to_apply":
- Include: how to submit, application process, email-to-apply, portal steps,
  required documents checklist for submission, contact person for applications,
  "send CV to…", "apply via…", "click apply and…", submission deadlines phrased
  as instructions (not the posting date alone).
- Exclude: role summary, responsibilities, requirements, benefits, company blurb,
  salary, location — those stay in "description".
- If the listing only has an external apply button/URL and no instructional text,
  set "how_to_apply" to "".

Rules for "description":
- Keep ALL public details that are NOT application instructions.
- Do NOT leave application emails, "email us at", or step-by-step apply copy in
  "description" when you placed them in "how_to_apply".
- Preserve Markdown structure (headings, lists) for what remains.
- Never invent content. Never wrap the JSON in code fences.`

func parseHowToApplySplit(raw string) (HowToApplySplit, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return HowToApplySplit{}, fmt.Errorf("empty model response")
	}
	// Tolerate accidental fences.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var split HowToApplySplit
	if err := json.Unmarshal([]byte(raw), &split); err != nil {
		return HowToApplySplit{}, err
	}
	return split, nil
}

// balanceTruncate keeps head + tail when s exceeds n runes so apply sections
// at the end of long postings are not dropped.
func balanceTruncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	// 60% head (role) + 40% tail (apply).
	head := (n * 3) / 5
	tail := n - head - 20 // room for separator
	if tail < 200 {
		tail = n / 3
		head = n - tail - 20
	}
	if head < 1 {
		return string(runes[:n])
	}
	return string(runes[:head]) + "\n\n[...]\n\n" + string(runes[len(runes)-tail:])
}
