package recipe

import "context"

// LLM is the text-completion seam the Generator uses. extraction.Extractor
// satisfies it via Complete(ctx, prompt) returning the model's text (JSON mode).
type LLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// SamplePage is one fetched sample used for generation and validation.
type SamplePage struct {
	URL  string
	HTML string
}
