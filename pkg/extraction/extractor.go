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
	Title          string   `json:"title"`
	Company        string   `json:"company"`
	Location       string   `json:"location"`
	Description    string   `json:"description"`
	ApplyURL       string   `json:"apply_url"`
	EmploymentType string   `json:"employment_type"`
	RemoteType     string   `json:"remote_type"`
	SalaryMin      string   `json:"salary_min"`
	SalaryMax      string   `json:"salary_max"`
	Currency       string   `json:"currency"`
	Seniority      string   `json:"seniority"`
	Skills         []string `json:"skills"`
	Roles          []string `json:"roles"`
	Benefits       []string `json:"benefits"`
	ContactName    string   `json:"contact_name"`
	ContactEmail   string   `json:"contact_email"`
	Department     string   `json:"department"`
	Industry       string   `json:"industry"`
	Education      string   `json:"education"`
	Experience     string   `json:"experience"`
	Deadline       string   `json:"deadline"`
}

const systemPrompt = `You are a job posting data extractor. Extract ALL available information from this job posting page. Output ONLY valid JSON, nothing else. If a field is not found, use empty string for text or empty array for lists.

Required fields:
- title: exact job title
- company: employer/organization name
- location: city, country or "Remote"
- description: comprehensive summary of the role, responsibilities and requirements (max 300 words)
- apply_url: application link if visible on the page

Compensation (estimate from context if not stated explicitly):
- salary_min: minimum salary as number string, estimate based on role/location/seniority if not stated
- salary_max: maximum salary as number string, estimate if not stated
- currency: salary currency code (e.g. "USD", "KES", "ZAR", "GBP")

Classification:
- employment_type: "full-time", "part-time", "contract", "internship", or "freelance"
- remote_type: "remote", "hybrid", "onsite", or ""
- seniority: "intern", "junior", "mid", "senior", "lead", "manager", "director", "executive"
- department: department or team name
- industry: industry sector (e.g. "technology", "finance", "healthcare")

Skills and requirements:
- skills: array of specific technical and soft skills mentioned (e.g. ["Python", "SQL", "Project Management"])
- roles: array of role categories this fits (e.g. ["Software Engineering", "Backend Development"])
- education: minimum education requirement (e.g. "Bachelor's in Computer Science")
- experience: years of experience required (e.g. "3-5 years")

Benefits and perks:
- benefits: array of benefits mentioned (e.g. ["Health Insurance", "Remote Work", "Stock Options"])

Contact information:
- contact_name: hiring manager or recruiter name if mentioned
- contact_email: contact email if mentioned
- deadline: application deadline date if mentioned`

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

const maxEmbedChars = 2000

// Embed generates an embedding vector for the given text by calling Ollama's
// /api/embeddings endpoint. Input text is truncated to 2000 characters.
// Returns nil and an error if Ollama is unreachable.
func (e *Extractor) Embed(ctx context.Context, text string) ([]float32, error) {
	text = truncateText(text, maxEmbedChars)

	reqBody := map[string]interface{}{
		"model":  e.model,
		"prompt": text,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embeddings", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("embed: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embed: ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var embResp struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}

	if len(embResp.Embedding) == 0 {
		return nil, fmt.Errorf("embed: empty embedding returned")
	}

	return embResp.Embedding, nil
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
