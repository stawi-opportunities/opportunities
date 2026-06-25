package autoapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// LLMClient is the narrow interface the LLMFormSubmitter needs — a
// single chat completion call. Production wiring uses the OpenAI client
// in apps/autoapply/cmd.
type LLMClient interface {
	// Complete sends a system + user prompt and returns the model's reply.
	Complete(ctx context.Context, system, user string) (string, error)
}

// LLMFormSubmitter is the Tier-2 generic handler. It fetches the apply
// page HTML via headless browser, extracts form fields using an LLM
// prompt, fills them, and submits. Handles any URL not matched by a
// Tier-1 ATS handler.
type LLMFormSubmitter struct {
	client browser.ApplyClient
	llm    LLMClient
}

// NewLLMFormSubmitter wires the handler.
func NewLLMFormSubmitter(client browser.ApplyClient, llm LLMClient) *LLMFormSubmitter {
	return &LLMFormSubmitter{client: client, llm: llm}
}

func (s *LLMFormSubmitter) Name() string { return "llm_form" }

// CanHandle returns true for any non-empty URL — this is the catch-all
// tier. It should be registered last in the Registry.
func (s *LLMFormSubmitter) CanHandle(_ domain.SourceType, applyURL string) bool {
	return strings.TrimSpace(applyURL) != "" && s.llm != nil
}

var systemPrompt = `You are a job application form assistant. Given simplified form HTML and a candidate profile, return a JSON object mapping CSS selectors to values that should be filled. Only include fields you can confidently fill from the profile. Do not include file upload inputs. Respond with valid JSON only, no markdown.`

func (s *LLMFormSubmitter) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
	html, err := s.client.GetHTML(ctx, req.ApplyURL)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("llm_form: get html: %w", err)
	}

	formHTML := extractFormHTML(html, 4000)
	if formHTML == "" {
		return SubmitResult{Method: "skipped", SkipReason: "no_form_found"}, nil
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
		req.FullName, req.Email, req.Phone, req.Location, req.CurrentTitle, req.Skills, formHTML)

	reply, err := s.llm.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("llm_form: complete: %w", err)
	}

	fieldMap, err := parseFieldMap(reply)
	if err != nil || len(fieldMap) == 0 {
		return SubmitResult{Method: "skipped", SkipReason: "llm_no_fields"}, nil
	}

	fillErr := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
		URL:        req.ApplyURL,
		TextFields: fieldMap,
		FileField:  "input[type='file']",
		FileBytes:  req.CVBytes,
		FileName:   req.CVFilename,
		SubmitSel:  "button[type='submit'], input[type='submit']",
	})
	if fillErr != nil {
		if errors.Is(fillErr, browser.ErrCAPTCHA) {
			return SubmitResult{Method: "skipped", SkipReason: "captcha"}, nil
		}
		if errors.Is(fillErr, browser.ErrElementNotFound) {
			return SubmitResult{Method: "skipped", SkipReason: "unsupported"}, nil
		}
		if errors.Is(fillErr, browser.ErrSubmitNotConfirmed) {
			return SubmitResult{Method: "skipped", SkipReason: "not_confirmed"}, nil
		}
		return SubmitResult{}, fillErr
	}

	return SubmitResult{Method: "llm_form"}, nil
}

// formStartRE matches an opening <form ...> tag. Used to locate every
// candidate form in the page so we can score and pick the application
// form rather than always taking the first (which is often the search
// box at the top of the page).
var formStartRE = regexp.MustCompile(`(?i)<form\b`)

// extractFormHTML strips non-form content and truncates to maxLen runes.
// Picks the form whose attributes / nearby text suggest "application"
// over the noisy login/search/newsletter forms common at the top of
// careers pages.
func extractFormHTML(html string, maxLen int) string {
	type candidate struct {
		body  string
		score int
	}
	var picks []candidate

	for _, idx := range formStartRE.FindAllStringIndex(html, -1) {
		start := idx[0]
		// Find the matching </form> after this <form>.
		rest := strings.ToLower(html[start:])
		end := strings.Index(rest, "</form>")
		if end < 0 {
			continue
		}
		body := html[start : start+end+len("</form>")]
		picks = append(picks, candidate{body: body, score: scoreForm(body)})
	}
	if len(picks) == 0 {
		return ""
	}

	// Pick highest-scoring form.
	best := picks[0]
	for _, p := range picks[1:] {
		if p.score > best.score {
			best = p
		}
	}

	snippet := stripTag(best.body, "script")
	snippet = stripTag(snippet, "style")

	if len(snippet) > maxLen {
		// Truncate by runes to avoid splitting a UTF-8 sequence.
		runes := []rune(snippet)
		if len(runes) > maxLen {
			runes = runes[:maxLen]
		}
		snippet = string(runes)
	}
	return snippet
}

// scoreForm assigns a weight to a candidate <form> body. Application
// forms typically contain many <input> fields and keywords like
// "apply", "resume", "cover letter".
func scoreForm(body string) int {
	lower := strings.ToLower(body)
	score := 0
	score += strings.Count(lower, "<input") * 2
	score += strings.Count(lower, "<textarea") * 3
	score += strings.Count(lower, "<select") * 2
	for _, kw := range []string{"apply", "application", "resume", "cv", "cover letter", "first name", "last name"} {
		if strings.Contains(lower, kw) {
			score += 5
		}
	}
	for _, anti := range []string{"search", "subscribe", "newsletter", "login", "sign in"} {
		if strings.Contains(lower, anti) {
			score -= 4
		}
	}
	return score
}

// stripTag removes every <tag>...</tag> pair from s in a single pass.
// Case-insensitive on the tag name; tag content (including nested
// tags) is dropped wholesale.
func stripTag(s, tag string) string {
	open := "<" + strings.ToLower(tag)
	closeT := "</" + strings.ToLower(tag) + ">"
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	lower := strings.ToLower(s)
	for i < len(s) {
		o := strings.Index(lower[i:], open)
		if o < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+o])
		c := strings.Index(lower[i+o:], closeT)
		if c < 0 {
			break
		}
		i = i + o + c + len(closeT)
	}
	return b.String()
}

// parseFieldMap unmarshals the LLM reply into a map[string]string. The
// LLM is instructed to return raw JSON; markdown fences are stripped
// before parsing. Object values are coerced to their JSON string form
// so a model that returns {"a": 5} doesn't blow up the unmarshal.
func parseFieldMap(reply string) (map[string]string, error) {
	s := strings.TrimSpace(reply)
	if strings.HasPrefix(s, "```") {
		// Drop the first line ("```json" or "```") and the last "```".
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("parseFieldMap: %w", err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		var sv string
		if err := json.Unmarshal(v, &sv); err == nil {
			out[k] = sv
			continue
		}
		// Number / bool / null → use raw JSON literal.
		out[k] = strings.Trim(string(v), `"`)
	}
	return out, nil
}
