package recipe

import "context"

// Fetcher abstracts an HTTP GET so the Executor can be driven by a real client
// in production and by an httptest server in tests. The production adapter
// (Plan 3) wraps the crawler's httpx.Client.
type Fetcher interface {
	Get(ctx context.Context, url string) (body []byte, status int, err error)
}

// HeaderedFetcher is the optional extension a Fetcher implements to send custom
// request headers (recipe list.headers — e.g. an API auth/JWT/app-id header).
// The Executor uses it when present and the recipe declares headers; a Fetcher
// without it falls back to plain Get and the headers are ignored.
type HeaderedFetcher interface {
	GetH(ctx context.Context, url string, headers map[string]string) (body []byte, status int, err error)
}
