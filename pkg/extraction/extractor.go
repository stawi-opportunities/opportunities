// Package extraction provides AI-based job field extraction over any
// OpenAI-compatible chat completions endpoint (Cloudflare AI Gateway,
// Groq, OpenAI, Ollama 0.3+, etc.).
package extraction

import (
	"context"
	"encoding/json"
	"fmt"
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

const systemPrompt = `You are a job posting data extractor.

Output ONLY a single JSON object. Do not nest fields under group headings. Every key below must appear at the top level of the object. Missing values use "" for strings, [] for arrays, 0 for numbers, false for booleans.

Keys (all top-level):
title (string)
company (string)
location (string)
description (string, max 500 words)
apply_url (string)
salary_min (string of digits)
salary_max (string of digits)
currency (string, e.g. "USD")
employment_type ("full-time"|"part-time"|"contract"|"internship"|"freelance"|"")
remote_type ("remote"|"hybrid"|"onsite"|"")
seniority ("intern"|"junior"|"mid"|"senior"|"lead"|"manager"|"director"|"executive"|"")
department (string)
industry (string)
skills (array of strings)
roles (array of strings)
education (string)
experience (string)
required_skills (array of strings)
nice_to_have_skills (array of strings)
tools_frameworks (array of strings)
benefits (array of strings)
contact_name (string)
contact_email (string)
deadline (string)
urgency_level ("urgent"|"normal"|"low")
urgency_signals (array of strings)
hiring_timeline ("immediate"|"2-4 weeks"|"1-3 months"|"")
interview_stages (number)
has_take_home (boolean)
funnel_complexity ("low"|"medium"|"high"|"")
company_size ("startup"|"small"|"medium"|"large"|"enterprise"|"")
funding_stage ("bootstrapped"|"seed"|"series_a"|"series_b"|"public"|"")
geo_restrictions ("global"|"us_only"|"emea"|"africa"|"")
timezone_req (string)
application_type ("ats"|"email"|"portal"|"direct"|"")
ats_platform ("greenhouse"|"lever"|"workday"|"")
role_scope ("ic"|"manager"|"hybrid"|"executive"|"")
team_size ("solo"|"small_team"|"large_team"|"")
reports_to (string)`

const maxContentChars = 4000
const extractionTimeout = 10 * time.Minute // was 120s — let AI finish on CPU

// Extractor calls an OpenAI-compatible chat completions endpoint to extract
// structured job fields from HTML. Works against Cloudflare AI Gateway,
// Groq, OpenAI, and Ollama 0.3+ unchanged.
type Extractor struct {
	baseURL string
	apiKey  string
	model   string

	embeddingBaseURL string
	embeddingAPIKey  string
	embeddingModel   string

	client *http.Client
}

// New builds an Extractor from a Config. BaseURL and Model are required.
// Embedding* fields are optional; when EmbeddingBaseURL is empty, Embed()
// returns (nil, nil) and callers fall back to a non-vector path.
func New(cfg Config) *Extractor {
	return &Extractor{
		baseURL:          strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:           cfg.APIKey,
		model:            cfg.Model,
		embeddingBaseURL: strings.TrimRight(cfg.EmbeddingBaseURL, "/"),
		embeddingAPIKey:  cfg.EmbeddingAPIKey,
		embeddingModel:   cfg.EmbeddingModel,
		client:           &http.Client{Timeout: extractionTimeout},
	}
}

// NewExtractor is the legacy two-arg constructor. Keeps existing callers
// compiling while we migrate everyone to New(Config).
func NewExtractor(baseURL, model string) *Extractor {
	return New(Config{BaseURL: baseURL, Model: model})
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

	content, err := e.chat(ctx, prompt, true)
	if err != nil {
		return nil, fmt.Errorf("extraction: %w", err)
	}
	fields, err := parseResponse(content)
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

	content, err := e.chat(ctx, prompt, true)
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}
	links, err := parseLinksResponse(content, pageURL)
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

	content, err := e.chat(ctx, prompt, true)
	if err != nil {
		return nil, fmt.Errorf("discover-sites: %w", err)
	}
	var result struct {
		Sites []DiscoveredSite `json:"sites"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
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

// Embed generates an embedding vector for the given text via the OpenAI-
// compatible /v1/embeddings endpoint. Input text is truncated to 2000
// characters. Returns (nil, nil) — silently — when embeddings aren't
// configured so callers can skip storing the vector without treating the
// pipeline step as failed.
func (e *Extractor) Embed(ctx context.Context, text string) ([]float32, error) {
	embedCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	text = truncateText(text, maxEmbedChars)
	return e.embed(embedCtx, text)
}

// Prompt sends an arbitrary system + user content pair to the LLM and
// returns the raw content string. Generic escape hatch for handlers that
// need a custom prompt (e.g. validation, scoring).
func (e *Extractor) Prompt(ctx context.Context, systemPrompt, userContent string) (string, error) {
	return e.chat(ctx, systemPrompt+"\n\n"+userContent, true)
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
