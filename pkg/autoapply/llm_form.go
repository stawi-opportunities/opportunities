package autoapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// LLMClient is the narrow interface the LLMFormSubmitter needs —
// a single chat completion call. Production wiring uses extraction.Extractor.
type LLMClient interface {
	// Complete sends a system + user prompt and returns the model's reply.
	Complete(ctx context.Context, system, user string) (string, error)
}

// LLMFormSubmitter is the Tier-2 generic handler. It fetches the apply
// page HTML via headless browser, extracts form fields using an LLM
// prompt, fills them, and submits. Handles any URL not matched by a
// Tier-1 ATS handler.
type LLMFormSubmitter struct {
	client *browser.ApplyClient
	llm    LLMClient
}

// NewLLMFormSubmitter wires the handler.
func NewLLMFormSubmitter(client *browser.ApplyClient, llm LLMClient) *LLMFormSubmitter {
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

	fillErr := s.client.FillAndSubmit(
		ctx,
		req.ApplyURL,
		fieldMap,
		"input[type='file']",
		req.CVBytes,
		req.CVFilename,
		"button[type='submit'], input[type='submit']",
	)
	if fillErr != nil {
		if errors.Is(fillErr, browser.ErrCAPTCHA) {
			return SubmitResult{Method: "skipped", SkipReason: "captcha"}, nil
		}
		if errors.Is(fillErr, browser.ErrElementNotFound) {
			return SubmitResult{Method: "skipped", SkipReason: "unsupported"}, nil
		}
		return SubmitResult{}, fillErr
	}

	return SubmitResult{Method: "llm_form"}, nil
}

// extractFormHTML strips non-form content and truncates to maxLen runes.
// Keeps only tags related to forms (form, input, label, select, textarea,
// button) to minimise the LLM prompt size.
func extractFormHTML(html string, maxLen int) string {
	var sb strings.Builder
	lower := strings.ToLower(html)

	// Find the first <form element.
	start := strings.Index(lower, "<form")
	if start < 0 {
		return ""
	}
	// Find the closing </form>.
	end := strings.LastIndex(lower, "</form>")
	if end < 0 || end <= start {
		end = len(html)
	} else {
		end += len("</form>")
	}

	snippet := html[start:end]
	// Strip script/style tags.
	snippet = removeTagContent(snippet, "script")
	snippet = removeTagContent(snippet, "style")

	for _, r := range snippet {
		if sb.Len() >= maxLen {
			break
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// removeTagContent removes all content between <tag> and </tag> (case-insensitive).
func removeTagContent(s, tag string) string {
	lower := strings.ToLower(s)
	open := "<" + tag
	close := "</" + tag + ">"
	var sb strings.Builder
	for {
		start := strings.Index(strings.ToLower(s), open)
		if start < 0 {
			sb.WriteString(s)
			break
		}
		sb.WriteString(s[:start])
		end := strings.Index(strings.ToLower(s[start:]), close)
		if end < 0 {
			break
		}
		s = s[start+end+len(close):]
		lower = strings.ToLower(s)
		_ = lower
	}
	return sb.String()
}

// parseFieldMap unmarshals the LLM reply into a map[string]string.
// The LLM is instructed to return raw JSON; any markdown fences are
// stripped before parsing.
func parseFieldMap(reply string) (map[string]string, error) {
	s := strings.TrimSpace(reply)
	// Strip ```json ... ``` fences if the model ignored instructions.
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) > 2 {
			s = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("parseFieldMap: %w", err)
	}
	return m, nil
}
