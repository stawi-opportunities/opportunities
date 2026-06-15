package autoapply

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
)

// FieldAnswerProfile is the candidate data the LLM draws on to answer the
// open-ended application questions a form may ask.
type FieldAnswerProfile struct {
	FullName     string
	Email        string
	Phone        string
	Location     string
	CurrentTitle string
	Skills       string
	CoverLetter  string
}

// answerFieldsSystemPrompt steers the model toward the *extra* questions a
// form asks (work authorization, years of experience, salary, screening
// questions) and away from the standard identity/resume fields that the
// caller fills from structured profile data.
var answerFieldsSystemPrompt = `You are a job application assistant. Given the simplified HTML of an application form and a candidate profile, return a JSON object mapping CSS selectors to the values that should be filled. Only answer the additional/custom application questions (work authorization, years of experience, salary expectation, "why do you want to work here", screening questions). Do NOT answer name, email, phone, or resume/file fields — those are filled separately. Omit any field you cannot answer confidently from the profile. Respond with valid JSON only, no markdown.`

// AnswerFormFields isolates the application form in pageHTML, asks the LLM
// to answer the custom questions from the profile, and returns a selector
// to value map. It never errors on "nothing to do": a missing form,
// unparseable reply, or nil LLM all yield an empty map so callers can
// merge unconditionally. A non-nil error means the LLM call itself failed.
func AnswerFormFields(ctx context.Context, llm LLMClient, pageHTML string, p FieldAnswerProfile) (map[string]string, error) {
	if llm == nil {
		return map[string]string{}, nil
	}
	formHTML := extractFormHTML(pageHTML, 4000)
	if formHTML == "" {
		return map[string]string{}, nil
	}

	userPrompt := fmt.Sprintf(`Candidate profile:
- Name: %s
- Email: %s
- Phone: %s
- Location: %s
- Current title: %s
- Skills: %s

Form HTML:
%s

Return JSON: {"selector": "value", ...}`,
		p.FullName, p.Email, p.Phone, p.Location, p.CurrentTitle, p.Skills, formHTML)

	reply, err := llm.Complete(ctx, answerFieldsSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("answer form fields: %w", err)
	}

	fields, err := parseFieldMap(reply)
	if err != nil {
		// Unparseable reply is treated as "no confident answers" rather
		// than a hard failure — the static fields still submit.
		return map[string]string{}, nil
	}
	return fields, nil
}

// answerFieldsStructuredPrompt instructs the model to answer ONLY the
// listed fields, using the exact selectors provided. This avoids the
// hallucinated-selector failure mode of dumping raw HTML.
var answerFieldsStructuredPrompt = `You are a job application assistant. You are given a candidate profile and a JSON array of the form's fillable fields, each with a "selector", "label" (the question), "type", "required", and optional "options". Return a JSON object mapping selector to the value to fill.

Rules:
- Use ONLY selectors that appear in the provided fields. Never invent selectors.
- Do NOT answer identity fields (name, first/last name, email, phone) — they are filled separately.
- For "select", "radio", and "combobox" fields, the value MUST be one of the provided "options" verbatim.
- For "checkbox" fields, return "true" to check it or "false" to leave it unchecked (check required acknowledgements/consents).
- For free-text fields, give a concise, professional answer grounded in the profile.
- Prioritise required fields. Omit any field you cannot answer confidently from the profile.
- Respond with valid JSON only, no markdown.`

// AnswerFieldsStructured asks the LLM to answer the supplied form fields
// from the profile, then drops any selector the model returned that is not
// in the provided field set (defence against hallucinated selectors).
// Returns an empty map (not an error) when there is nothing to answer.
func AnswerFieldsStructured(ctx context.Context, llm LLMClient, fields []browser.FieldDescriptor, p FieldAnswerProfile) (map[string]string, error) {
	if llm == nil || len(fields) == 0 {
		return map[string]string{}, nil
	}

	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.Selector] = true
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return map[string]string{}, nil
	}

	userPrompt := fmt.Sprintf(`Candidate profile:
- Name: %s
- Email: %s
- Phone: %s
- Location: %s
- Current title: %s
- Skills: %s
- Cover letter: %s

Form fields (JSON):
%s

Return JSON: {"selector": "value", ...}`,
		p.FullName, p.Email, p.Phone, p.Location, p.CurrentTitle, p.Skills, p.CoverLetter, string(fieldsJSON))

	reply, err := llm.Complete(ctx, answerFieldsStructuredPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("answer fields: %w", err)
	}

	raw, err := parseFieldMap(reply)
	if err != nil {
		return map[string]string{}, nil
	}
	// Keep only selectors that actually exist on the form.
	out := make(map[string]string, len(raw))
	for sel, val := range raw {
		if known[sel] && val != "" {
			out[sel] = val
		}
	}
	return out, nil
}
