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
- description: comprehensive summary of the role, responsibilities and requirements (max 1000 words)
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

const maxContentChars = 4000
const extractionTimeout = 120 * time.Second

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

const discoverLinksPrompt = `You are a web page analyzer. Given the HTML content of a job board listing page, identify ALL URLs that link to individual job posting detail pages.

Return ONLY a JSON array of full URLs. Do not include:
- Navigation links (about, contact, login, categories)
- Pagination links
- Social media links
- Employer/company profile links

Only include links that lead to a page describing a single specific job opening.

Also identify the "next page" URL if pagination exists. Return it as the last element with prefix "NEXT:"

Page URL: %s

Page content:
%s`

// DiscoverLinks uses AI to find job detail links on a listing page.
// It returns absolute URLs found on the page and optionally a "next page" URL
// (returned as the last element prefixed with "NEXT:").
func (e *Extractor) DiscoverLinks(ctx context.Context, rawHTML string, pageURL string) ([]string, error) {
	text := stripHTMLKeepLinks(rawHTML)
	text = truncateText(text, maxContentChars)

	prompt := fmt.Sprintf(discoverLinksPrompt, pageURL, text)

	reqBody := map[string]interface{}{
		"model":  e.model,
		"prompt": prompt,
		"stream": false,
		"format": "json",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("discover: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("discover: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover: ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("discover: ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("discover: decode ollama response: %w", err)
	}

	links, err := parseLinksResponse(ollamaResp.Response, pageURL)
	if err != nil {
		return nil, fmt.Errorf("discover: parse model output: %w", err)
	}

	return links, nil
}

// parseLinksResponse unmarshals a JSON array (or object with a "urls"/"links" key)
// from the model and resolves relative URLs against pageURL.
func parseLinksResponse(raw string, pageURL string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty response from model")
	}

	// Try direct array first.
	var links []string
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		// Try as object with common keys.
		var obj map[string]json.RawMessage
		if err2 := json.Unmarshal([]byte(raw), &obj); err2 != nil {
			return nil, fmt.Errorf("unmarshal links: %w", err)
		}
		for _, key := range []string{"urls", "links", "job_links", "results"} {
			if v, ok := obj[key]; ok {
				if err3 := json.Unmarshal(v, &links); err3 == nil {
					break
				}
			}
		}
	}

	// Resolve relative URLs.
	baseURL, _ := resolveBaseURL(pageURL)
	var resolved []string
	for _, link := range links {
		abs := resolveURL(link, baseURL)
		if abs != "" {
			resolved = append(resolved, abs)
		}
	}

	return resolved, nil
}

const discoverSitesPrompt = `You are a job board discovery engine. Given the content of a web page, identify any EXTERNAL job boards, career sites, or job listing platforms linked from this page that we should also crawl for job postings.

Return ONLY a JSON object with this structure:
{"sites": [{"url": "https://...", "name": "Site Name", "country": "XX", "type": "job_board"}]}

Rules:
- Only include sites that host job listings (job boards, career pages, recruitment platforms)
- Use the ROOT URL of the job board (e.g. "https://www.jobberman.com" not a specific job page)
- Include the 2-letter country code if identifiable, empty string if global
- Type should be "job_board", "career_page", "recruitment_agency", or "aggregator"
- Do NOT include the current site itself
- Do NOT include social media (linkedin, twitter, facebook)
- Do NOT include generic sites (google, wikipedia)
- It's OK to return an empty array if no job boards are found

Page URL: %s

Page content:
%s`

// DiscoveredSite represents a new job site found during crawling.
type DiscoveredSite struct {
	URL     string `json:"url"`
	Name    string `json:"name"`
	Country string `json:"country"`
	Type    string `json:"type"`
}

// DiscoverSites uses AI to find links to other job boards on a page.
func (e *Extractor) DiscoverSites(ctx context.Context, rawHTML string, pageURL string) ([]DiscoveredSite, error) {
	text := stripHTMLKeepLinks(rawHTML)
	text = truncateText(text, maxContentChars)

	prompt := fmt.Sprintf(discoverSitesPrompt, pageURL, text)

	reqBody := map[string]interface{}{
		"model":  e.model,
		"prompt": prompt,
		"stream": false,
		"format": "json",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("discover-sites: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("discover-sites: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover-sites: ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("discover-sites: status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("discover-sites: decode: %w", err)
	}

	var result struct {
		Sites []DiscoveredSite `json:"sites"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(ollamaResp.Response)), &result); err != nil {
		return nil, fmt.Errorf("discover-sites: parse: %w", err)
	}

	return result.Sites, nil
}

// resolveBaseURL extracts the scheme+host from a URL for resolving relative links.
func resolveBaseURL(pageURL string) (string, string) {
	// Find scheme://host
	idx := strings.Index(pageURL, "://")
	if idx < 0 {
		return pageURL, ""
	}
	rest := pageURL[idx+3:]
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return pageURL, ""
	}
	origin := pageURL[:idx+3+slashIdx]
	// Path up to last slash for relative resolution
	lastSlash := strings.LastIndex(pageURL, "/")
	pathBase := pageURL[:lastSlash+1]
	_ = pathBase
	return origin, pathBase
}

// resolveURL resolves a possibly-relative URL against the origin.
func resolveURL(link string, origin string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return link
	}
	if strings.HasPrefix(link, "//") {
		return "https:" + link
	}
	if strings.HasPrefix(link, "/") {
		return origin + link
	}
	return origin + "/" + link
}

// stripHTMLKeepLinks removes script/style blocks and most HTML tags but preserves
// anchor tags as markdown-style [text](url) so the AI can see link text and URLs.
func stripHTMLKeepLinks(html string) string {
	// Remove script and style blocks.
	scriptRe := regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	html = scriptRe.ReplaceAllString(html, " ")

	// Convert <a href="url">text</a> to [text](url).
	anchorRe := regexp.MustCompile(`(?is)<a\s[^>]*href=["']([^"']*)["'][^>]*>(.*?)</a>`)
	html = anchorRe.ReplaceAllStringFunc(html, func(match string) string {
		parts := anchorRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		href := parts[1]
		text := stripAllTags(parts[2])
		text = strings.TrimSpace(text)
		return fmt.Sprintf("[%s](%s)", text, href)
	})

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

// stripAllTags removes all HTML tags from a string.
func stripAllTags(s string) string {
	tagRe := regexp.MustCompile(`<[^>]+>`)
	return tagRe.ReplaceAllString(s, " ")
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
