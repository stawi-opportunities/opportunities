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

	// Urgency & hiring intent
	UrgencyLevel   string   `json:"urgency_level"`
	UrgencySignals []string `json:"urgency_signals"`
	HiringTimeline string   `json:"hiring_timeline"`

	// Hiring funnel complexity
	InterviewStages  int    `json:"interview_stages"`
	HasTakeHome      bool   `json:"has_take_home"`
	FunnelComplexity string `json:"funnel_complexity"`

	// Company profile
	CompanySize  string `json:"company_size"`
	FundingStage string `json:"funding_stage"`

	// Skills classification
	RequiredSkills   []string `json:"required_skills"`
	NiceToHaveSkills []string `json:"nice_to_have_skills"`
	ToolsFrameworks  []string `json:"tools_frameworks"`

	// Work model details
	GeoRestrictions string `json:"geo_restrictions"`
	TimezoneReq     string `json:"timezone_req"`

	// Application channel
	ApplicationType string `json:"application_type"`
	ATSPlatform     string `json:"ats_platform"`

	// Role clarity
	RoleScope string `json:"role_scope"`
	TeamSize  string `json:"team_size"`
	ReportsTo string `json:"reports_to"`
}

const systemPrompt = `You are a job posting data extractor. Output ONLY valid JSON. If a field is not found, use "" for strings, [] for arrays, 0 for numbers, false for booleans.

Extract these fields:

title, company, location, description (max 500 words), apply_url

Compensation: salary_min (number string), salary_max (number string), currency (e.g. "USD")

Classification: employment_type ("full-time"/"part-time"/"contract"/"internship"/"freelance"), remote_type ("remote"/"hybrid"/"onsite"/""), seniority ("intern"/"junior"/"mid"/"senior"/"lead"/"manager"/"director"/"executive"), department, industry

Skills: skills (array of all skills), roles (array of role categories), education, experience

required_skills (must-have skills array), nice_to_have_skills (preferred skills array), tools_frameworks (specific tech stack array)

Benefits: benefits (array), contact_name, contact_email, deadline

Urgency: urgency_level ("urgent" if words like "immediately","ASAP","urgent"; "normal" otherwise; "low" if no rush), urgency_signals (array of urgency phrases found), hiring_timeline ("immediate"/"2-4 weeks"/"1-3 months"/"")

Hiring funnel: interview_stages (estimated number, 0 if unknown), has_take_home (true if take-home/assignment mentioned), funnel_complexity ("low" if 1-2 stages, "medium" if 3-4, "high" if 5+)

Company: company_size ("startup"/"small"/"medium"/"large"/"enterprise" from context), funding_stage ("bootstrapped"/"seed"/"series_a"/"series_b"/"public"/"")

Work model: geo_restrictions ("global"/"us_only"/"emea"/"africa"/"" from location requirements), timezone_req (e.g. "UTC+/-3","US hours","any","")

Application: application_type ("ats" if ATS link,"email" if email apply,"portal","direct"), ats_platform ("greenhouse"/"lever"/"workday"/"" from URL patterns)

Role: role_scope ("ic"/"manager"/"hybrid"/"executive"), team_size ("solo"/"small_team"/"large_team"/""), reports_to (e.g. "CTO","VP Engineering","")`

const maxContentChars = 4000
const extractionTimeout = 10 * time.Minute // was 120s — let AI finish on CPU

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

// scriptContentRe matches <script type="application/ld+json">...</script>.
var scriptContentRe = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)

// nextDataRe matches <script id="__NEXT_DATA__">...</script>.
var nextDataRe = regexp.MustCompile(`(?is)<script[^>]*id=["']__NEXT_DATA__["'][^>]*>(.*?)</script>`)

// windowDataRe matches any script block containing common SPA state variable patterns.
var windowDataRe = regexp.MustCompile(`(?is)<script[^>]*>(.*?(?:__INITIAL_STATE__|__NUXT__|__DATA__|__NEXT_DATA__|window\.jobs|window\.listings).*?)</script>`)

// extractEmbeddedJSON finds all embedded JSON data blobs in script tags and
// returns them concatenated with newlines. Returns empty string if none found.
func extractEmbeddedJSON(rawHTML string) string {
	var parts []string

	for _, m := range scriptContentRe.FindAllStringSubmatch(rawHTML, -1) {
		if content := strings.TrimSpace(m[1]); content != "" {
			parts = append(parts, content)
		}
	}

	for _, m := range nextDataRe.FindAllStringSubmatch(rawHTML, -1) {
		if content := strings.TrimSpace(m[1]); content != "" {
			parts = append(parts, content)
		}
	}

	for _, m := range windowDataRe.FindAllStringSubmatch(rawHTML, -1) {
		if content := strings.TrimSpace(m[1]); content != "" {
			parts = append(parts, content)
		}
	}

	return strings.Join(parts, "\n")
}

// HasVisibleContent returns true if the stripped HTML contains more than 200
// characters of text content. A false result on a page with embedded JSON
// indicates a JS-rendered page where the JSON is the primary content source.
func HasVisibleContent(rawHTML string) bool {
	return len([]rune(stripHTML(rawHTML))) > 200
}

// Extract strips HTML from rawHTML, truncates it, sends it to Ollama, and returns
// the parsed job fields. Returns nil and an error if Ollama is unreachable or the
// model produces unparseable output.
func (e *Extractor) Extract(ctx context.Context, rawHTML string, pageURL string) (*JobFields, error) {
	// Try to extract embedded JSON data first (handles JS-rendered pages)
	embeddedJSON := extractEmbeddedJSON(rawHTML)

	var text string
	if embeddedJSON != "" {
		// Use embedded JSON + stripped HTML for maximum context
		text = embeddedJSON + "\n\n" + stripHTML(rawHTML)
	} else {
		text = stripHTML(rawHTML)
	}
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
	// Prepend any embedded JSON so the AI can find job URLs from SPA state data.
	embeddedJSON := extractEmbeddedJSON(rawHTML)
	var text string
	if embeddedJSON != "" {
		text = embeddedJSON + "\n\n" + stripHTMLKeepLinks(rawHTML)
	} else {
		text = stripHTMLKeepLinks(rawHTML)
	}
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
	// Use a shorter timeout for embeddings (5 minutes vs 10 for extraction)
	embedCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	text = truncateText(text, maxEmbedChars)

	reqBody := map[string]interface{}{
		"model":  e.model,
		"prompt": text,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(embedCtx, http.MethodPost, e.baseURL+"/api/embeddings", bytes.NewReader(bodyBytes))
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

// Prompt sends an arbitrary system prompt and user content to Ollama and returns
// the raw JSON response string. This is a generic escape hatch for handlers that
// need to call the LLM with a custom prompt (e.g. validation, scoring).
func (e *Extractor) Prompt(ctx context.Context, systemPrompt, userContent string) (string, error) {
	combined := systemPrompt + "\n\n" + userContent

	reqBody := map[string]interface{}{
		"model":  e.model,
		"prompt": combined,
		"stream": false,
		"format": "json",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("prompt: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("prompt: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("prompt: ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("prompt: ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("prompt: decode ollama response: %w", err)
	}

	return strings.TrimSpace(ollamaResp.Response), nil
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
