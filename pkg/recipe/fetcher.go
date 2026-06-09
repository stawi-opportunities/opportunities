package recipe

import "context"

// Fetcher abstracts an HTTP GET so the Executor can be driven by a real client
// in production and by an httptest server in tests. The production adapter
// (Plan 3) wraps the crawler's httpx.Client.
type Fetcher interface {
	Get(ctx context.Context, url string) (body []byte, status int, err error)
}
