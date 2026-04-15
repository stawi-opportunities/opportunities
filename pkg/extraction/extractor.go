// Package extraction provides AI-based job field extraction via Ollama.
package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// JobFields holds the structured fields extracted from a job posting page.
type JobFields struct {
	Title          string `json:"title"`
	Company        string `json:"company"`
	Location       string `json:"location"`
	Description    string `json:"description"`
	ApplyURL       string `json:"apply_url"`
	EmploymentType string `json:"employment_type"`
	RemoteType     string `json:"remote_type"`
	SalaryMin      string `json:"salary_min"`
	SalaryMax      string `json:"salary_max"`
	Currency       string `json:"currency"`
}

const systemPrompt = `You are a job posting data extractor. Given the text content of a job posting page, extract the following fields as JSON. If a field is not found, use an empty string. Only output valid JSON, nothing else.

Fields: title, company, location, description, apply_url, employment_type, remote_type, salary_min, salary_max, currency

The description should be a brief summary (max 200 words) of the job requirements and responsibilities.`

const maxContentChars = 3000
const extractionTimeout = 30 * time.Second

// Extractor calls an Ollama instance to extract structured job fields from HTML.
type Extractor struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewExtractor creates a new Extractor targeting the given Ollama base URL and model.
func NewExtractor(baseURL, model string) *Extractor {
	return &Extractor{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: extractionTimeout},
	}
}

// Extract strips HTML from rawHTML, truncates it, sends it to Ollama, and returns
// the parsed job fields. Returns nil and an error if Ollama is unreachable or the
// model produces unparseable output.
func (e *Extractor) Extract(ctx context.Context, rawHTML string, pageURL string) (*JobFields, error) {
	text := stripHTML(rawHTML)
	text = truncateText(text, maxContentChars)

	prompt := fmt.Sprintf("%s\n\nPage URL: %s\n\nPage content:\n%s", systemPrompt, pageURL, text)

	reqBody := map[string]interface{}{
		"model":  e.model,
		"prompt": prompt,
		"stream": false,
		"format": "json",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("extraction: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("extraction: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("extraction: ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("extraction: ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("extraction: decode ollama response: %w", err)
	}

	fields, err := parseResponse(ollamaResp.Response)
	if err != nil {
		return nil, fmt.Errorf("extraction: parse model output: %w", err)
	}

	return fields, nil
}

// stripHTML removes HTML tags and decodes common entities, returning plain text.
func stripHTML(html string) string {
	// Remove script and style blocks entirely (with their content).
	scriptRe := regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	html = scriptRe.ReplaceAllString(html, " ")

	// Remove all remaining tags.
	tagRe := regexp.MustCompile(`<[^>]+>`)
	html = tagRe.ReplaceAllString(html, " ")

	// Decode common HTML entities.
	html = strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&nbsp;", " ",
	).Replace(html)

	// Collapse whitespace.
	wsRe := regexp.MustCompile(`\s+`)
	html = wsRe.ReplaceAllString(html, " ")

	return strings.TrimSpace(html)
}

// truncateText returns the first n characters of s, or s itself if shorter.
func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// parseResponse unmarshals the JSON string produced by the model into JobFields.
func parseResponse(raw string) (*JobFields, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty response from model")
	}
	var fields JobFields
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("unmarshal job fields: %w", err)
	}
	return &fields, nil
}
