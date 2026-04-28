// Package extraction provides AI-based opportunity field extraction over any
// OpenAI-compatible chat completions endpoint (Cloudflare AI Gateway,
// Groq, OpenAI, Ollama 0.3+, etc.). It is two-stage: a small classifier
// call picks the kind when a source emits multiple, then a kind-specific
// prompt assembled from the opportunity registry runs the structured
// extraction.
package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// LLM is the minimal text-completion interface the extractor needs.
// Implementations send the prompt to a backend and return the model's
// response as raw text. The default implementation, returned by New,
// posts to an OpenAI-compatible /v1/chat/completions endpoint.
type LLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// universalPromptPrefix is prepended to every extraction prompt regardless
// of kind. It establishes the output contract — single JSON object, no
// fences, ISO codes, RFC3339 timestamps — so each kind's prompt only has
// to enumerate its own schema.
const universalPromptPrefix = `You are an extraction assistant. Read the provided HTML/Markdown
and produce a single JSON object that strictly matches the schema below.
Output ONLY the JSON object — no prose, no code fences. Empty/unknown
fields should be omitted (not "" or null) unless the schema explicitly
requires them. Use ISO 3166-1 alpha-2 for country codes and ISO 4217 for
currency codes. Dates are RFC3339 timestamps in UTC.`

const maxContentChars = 4000
const extractionTimeout = 10 * time.Minute // was 120s — let AI finish on CPU

// HTTPDoer is the minimal http.Client surface this package exercises.
// Frame's HTTPClientManager.Client(ctx) satisfies it; tests use stdlib.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Extractor calls an OpenAI-compatible chat completions endpoint to extract
// structured opportunity fields from HTML. Works against Cloudflare AI
// Gateway, Groq, OpenAI, and Ollama 0.3+ unchanged.
//
// The two-stage Extract method consults registry to assemble per-kind
// prompts. When registry is nil (legacy callers / tests not wired through
// the boot path) Extract falls back to the universal prefix only and
// classifier behaviour collapses to "kind=job by default".
type Extractor struct {
	baseURL string
	apiKey  string
	model   string

	embeddingBaseURL string
	embeddingAPIKey  string
	embeddingModel   string

	rerankBaseURL string
	rerankAPIKey  string
	rerankModel   string

	client HTTPDoer

	llm      LLM
	registry *opportunity.Registry
}

// New builds an Extractor from a Config. BaseURL and Model are required.
// Embedding* fields are optional; when EmbeddingBaseURL is empty, Embed()
// returns (nil, nil) and callers fall back to a non-vector path.
//
// Config.Registry is optional: when present the Extract method assembles
// per-kind prompts from registry specs; when absent the universal prefix
// alone is used and the classifier falls back to "job" as the single
// implicit kind.
func New(cfg Config) *Extractor {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: extractionTimeout}
	}
	e := &Extractor{
		baseURL:          strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:           cfg.APIKey,
		model:            cfg.Model,
		embeddingBaseURL: strings.TrimRight(cfg.EmbeddingBaseURL, "/"),
		embeddingAPIKey:  cfg.EmbeddingAPIKey,
		embeddingModel:   cfg.EmbeddingModel,
		rerankBaseURL:    strings.TrimRight(cfg.RerankBaseURL, "/"),
		rerankAPIKey:     cfg.RerankAPIKey,
		rerankModel:      cfg.RerankModel,
		client:           hc,
		registry:         cfg.Registry,
	}
	e.llm = chatLLM{e: e}
	return e
}

// NewExtractor is the legacy two-arg constructor. Keeps existing callers
// compiling while we migrate everyone to New(Config).
func NewExtractor(baseURL, model string) *Extractor {
	return New(Config{BaseURL: baseURL, Model: model})
}

// SetRegistry attaches an opportunity registry post-construction. Useful
// when the registry is loaded after the extractor has been wired into
// other components, or in tests that want to swap kinds.
func (e *Extractor) SetRegistry(reg *opportunity.Registry) {
	e.registry = reg
}

// SetLLM swaps the underlying LLM client. Tests use this to inject a
// fake that returns canned JSON without going through HTTP — the same
// seam the in-package tests already exploit by writing the field
// directly. Production code never calls this.
func (e *Extractor) SetLLM(llm LLM) {
	e.llm = llm
}

// chatLLM adapts the existing HTTP chat method to the LLM interface so
// the public Extract method can be tested with a fake LLM without going
// through HTTP.
type chatLLM struct{ e *Extractor }

func (c chatLLM) Complete(ctx context.Context, prompt string) (string, error) {
	return c.e.chat(ctx, prompt, true)
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

// Extract runs (a) classification when sourceKinds has 0 or >1 entries,
// then (b) the kind-specific extraction prompt. The result has Kind set
// and Attributes populated with kind-specific fields; universal fields
// land on the envelope.
//
// sourceKinds is the Source.Kinds list — what the connector declared
// it can produce. A single entry skips the classifier; an empty slice
// means "any registered kind"; more than one calls the classifier and
// validates its output against the allowed set.
func (e *Extractor) Extract(ctx context.Context, html string, sourceKinds []string) (*domain.ExternalOpportunity, error) {
	kind, err := e.pickKind(ctx, html, sourceKinds)
	if err != nil {
		return nil, err
	}
	prompt := e.buildPrompt(kind)
	raw, err := e.llm.Complete(ctx, prompt+"\n\nDocument:\n"+html)
	if err != nil {
		return nil, fmt.Errorf("extraction: %w", err)
	}
	opp, err := parseExtractionJSON(raw, kind)
	if err != nil {
		return nil, fmt.Errorf("extraction: parse model output: %w", err)
	}
	opp.Kind = kind
	return opp, nil
}

// buildPrompt assembles the extraction prompt for the given kind by
// gluing the kind's schema fragment from the registry onto the universal
// prefix. Falls back to the prefix alone when the registry is missing or
// the kind has no schema fragment.
func (e *Extractor) buildPrompt(kind string) string {
	if e.registry == nil {
		return universalPromptPrefix
	}
	spec := e.registry.Resolve(kind)
	if spec.ExtractionPrompt == "" {
		return universalPromptPrefix
	}
	return universalPromptPrefix + "\n\nSchema for kind=" + kind + ":\n" + spec.ExtractionPrompt
}

// pickKind decides which kind's extraction prompt to use. Single-kind
// sources skip the classifier entirely; empty sourceKinds means "any
// registered kind" (and falls back to "job" when no registry is wired).
// Multi-kind sources run a small classifier call.
func (e *Extractor) pickKind(ctx context.Context, html string, sourceKinds []string) (string, error) {
	if len(sourceKinds) == 1 {
		return sourceKinds[0], nil
	}
	if len(sourceKinds) == 0 {
		if e.registry == nil {
			return "job", nil
		}
		sourceKinds = e.registry.Known()
		if len(sourceKinds) == 1 {
			return sourceKinds[0], nil
		}
		if len(sourceKinds) == 0 {
			return "job", nil
		}
	}
	classifierPrompt := fmt.Sprintf(`Classify the document as one of: %s.
Output ONLY the classification string.`, strings.Join(sourceKinds, ", "))
	out, err := e.llm.Complete(ctx, classifierPrompt+"\n\n"+html)
	if err != nil {
		return "", fmt.Errorf("extraction classify: %w", err)
	}
	pick := strings.TrimSpace(strings.ToLower(out))
	for _, k := range sourceKinds {
		if pick == k {
			return k, nil
		}
	}
	return "", fmt.Errorf("classifier returned unknown kind %q (allowed: %v)", pick, sourceKinds)
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

// Rerank scores each text against the query with a cross-encoder and
// returns scores in the same order as texts. Returns (nil, nil) when no
// reranker is configured — matchers treat that as "skip stage 3" and fall
// back to the bi-encoder order.
//
// The wire format matches HuggingFace TEI's /rerank endpoint:
//
//	POST /rerank
//	{"query": "...", "texts": ["...", "..."]}
//	→ {"results": [{"index": 0, "score": 0.83}, ...]}
//
// TEI returns results sorted by score descending; we reorder by index so
// the slice lines up with the input texts.
func (e *Extractor) Rerank(ctx context.Context, query string, texts []string) ([]float32, error) {
	if e.rerankBaseURL == "" || len(texts) == 0 {
		return nil, nil
	}
	return e.rerank(ctx, query, texts)
}

// EmbedModelVersion returns the configured embedding model identifier.
// Empty when no embedding backend is configured.
func (e *Extractor) EmbedModelVersion() string {
	if e == nil || e.embeddingBaseURL == "" {
		return ""
	}
	return e.embeddingModel
}

// RerankerVersion returns a stable identifier for cache invalidation. The
// model string is sufficient — changing the model naturally invalidates
// every cached score without any other coordination.
func (e *Extractor) RerankerVersion() string {
	if e.rerankBaseURL == "" {
		return ""
	}
	return e.rerankModel
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

// universalEnvelopeKeys is the closed set of top-level JSON keys that map
// onto ExternalOpportunity envelope fields. Anything not in this set is
// stashed under Attributes for the kind-specific schema.
var universalEnvelopeKeys = map[string]struct{}{
	"title": {}, "description": {}, "issuing_entity": {}, "apply_url": {},
	"anchor_country": {}, "anchor_region": {}, "anchor_city": {},
	"remote": {}, "geo_scope": {}, "currency": {}, "amount_min": {}, "amount_max": {},
	"deadline": {}, "categories": {}, "lat": {}, "lon": {},
}

// parseExtractionJSON unmarshals the model's JSON object and splits its
// keys between the universal envelope and the kind-specific Attributes
// map. Empty Attributes collapse to nil so verify-stage diagnostics
// don't lie about what the model returned.
func parseExtractionJSON(raw, kind string) (*domain.ExternalOpportunity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty response from model")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("parse extraction JSON: %w", err)
	}
	opp := &domain.ExternalOpportunity{Kind: kind, Attributes: map[string]any{}}
	for k, v := range m {
		if _, ok := universalEnvelopeKeys[k]; !ok {
			opp.Attributes[k] = v
			continue
		}
		assignUniversal(opp, k, v)
	}
	if len(opp.Attributes) == 0 {
		opp.Attributes = nil
	}
	return opp, nil
}

// assignUniversal copies a single universal-envelope key off the parsed
// JSON map onto the strongly-typed envelope fields of an
// ExternalOpportunity. Type-assertion failures are silent — the verify
// stage downstream will catch any required fields that ended up zero.
func assignUniversal(opp *domain.ExternalOpportunity, k string, v any) {
	switch k {
	case "title":
		opp.Title, _ = v.(string)
	case "description":
		opp.Description, _ = v.(string)
	case "issuing_entity":
		opp.IssuingEntity, _ = v.(string)
	case "apply_url":
		opp.ApplyURL, _ = v.(string)
	case "remote":
		if b, ok := v.(bool); ok {
			opp.Remote = b
		}
	case "geo_scope":
		opp.GeoScope, _ = v.(string)
	case "currency":
		opp.Currency, _ = v.(string)
	case "amount_min":
		opp.AmountMin, _ = toFloat(v)
	case "amount_max":
		opp.AmountMax, _ = toFloat(v)
	case "deadline":
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				opp.Deadline = &t
			}
		}
	case "categories":
		if a, ok := v.([]any); ok {
			for _, x := range a {
				if s, ok := x.(string); ok {
					opp.Categories = append(opp.Categories, s)
				}
			}
		}
	case "anchor_country":
		ensureLoc(opp).Country, _ = v.(string)
	case "anchor_region":
		ensureLoc(opp).Region, _ = v.(string)
	case "anchor_city":
		ensureLoc(opp).City, _ = v.(string)
	case "lat":
		ensureLoc(opp).Lat, _ = toFloat(v)
	case "lon":
		ensureLoc(opp).Lon, _ = toFloat(v)
	}
}

// ensureLoc lazily allocates AnchorLocation so callers don't have to
// guard every field assignment.
func ensureLoc(opp *domain.ExternalOpportunity) *domain.Location {
	if opp.AnchorLocation == nil {
		opp.AnchorLocation = &domain.Location{}
	}
	return opp.AnchorLocation
}

// toFloat coerces the typical JSON number/string shapes the LLM returns
// into a float64. Returns (0, false) for shapes that can't be coerced —
// callers ignore the failure (verify will catch missing required fields).
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}
