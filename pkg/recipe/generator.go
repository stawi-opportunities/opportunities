package recipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// Generator synthesizes an extraction recipe for a source from sample pages,
// using the LLM once (with a bounded repair loop). It is the ONLY AI consumer;
// the resulting recipe runs LLM-free thereafter.
type Generator struct {
	llm         LLM
	fetcher     Fetcher
	reg         *opportunity.Registry
	maxAttempts int
	sampleChars int
}

// NewGenerator builds a Generator. maxAttempts bounds the parse/validate repair
// loop (>=1). Sample HTML is truncated to a sane size for the prompt.
func NewGenerator(llm LLM, f Fetcher, reg *opportunity.Registry, maxAttempts int) *Generator {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &Generator{llm: llm, fetcher: f, reg: reg, maxAttempts: maxAttempts, sampleChars: 6000}
}

// Generate fetches the sample URLs, asks the LLM to synthesize a recipe, and
// repairs it (bounded) until it parses and structurally validates. It returns
// the recipe plus the fetched samples (for the Validator's pass-rate gate).
func (g *Generator) Generate(ctx context.Context, src domain.Source, sampleURLs []string) (*Recipe, []SamplePage, error) {
	var samples []SamplePage
	for _, u := range sampleURLs {
		body, status, err := g.fetcher.Get(ctx, u)
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		samples = append(samples, SamplePage{URL: u, HTML: string(body)})
	}
	if len(samples) == 0 {
		return nil, nil, fmt.Errorf("recipe generation: no fetchable samples among %d URLs", len(sampleURLs))
	}

	prompt := g.buildGenerationPrompt(src, samples)
	var lastErr error
	for attempt := 0; attempt < g.maxAttempts; attempt++ {
		raw, err := g.llm.Complete(ctx, prompt)
		if err != nil {
			lastErr = err
			continue
		}
		rec, perr := parseRecipeJSON(raw)
		if perr != nil {
			lastErr = perr
			prompt = g.repairPrompt(prompt, perr.Error())
			continue
		}
		if verr := rec.Validate(); verr != nil {
			lastErr = verr
			prompt = g.repairPrompt(prompt, verr.Error())
			continue
		}
		return rec, samples, nil
	}
	return nil, samples, fmt.Errorf("recipe generation failed after %d attempts: %w", g.maxAttempts, lastErr)
}

// parseRecipeJSON strips markdown fences/prose and unmarshals into a Recipe.
func parseRecipeJSON(raw string) (*Recipe, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = s[3:]
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}
	if i := strings.IndexAny(s, "{["); i > 0 {
		s = s[i:]
	}
	s = strings.TrimSpace(s)
	var rec Recipe
	if err := json.Unmarshal([]byte(s), &rec); err != nil {
		return nil, fmt.Errorf("recipe JSON parse: %w", err)
	}
	return &rec, nil
}
