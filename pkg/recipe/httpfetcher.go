package recipe

import "context"

// HeaderedGetter is the subset of the crawler's httpx.Client the recipe
// Fetcher needs. *httpx.Client satisfies it (Get(ctx, url, headers)).
type HeaderedGetter interface {
	Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error)
}

// HTTPFetcher adapts a HeaderedGetter to the recipe.Fetcher interface (no
// headers). This is the production Fetcher; tests use httptest directly.
type HTTPFetcher struct{ g HeaderedGetter }

func NewHTTPFetcher(g HeaderedGetter) *HTTPFetcher { return &HTTPFetcher{g: g} }

func (f *HTTPFetcher) Get(ctx context.Context, url string) ([]byte, int, error) {
	return f.g.Get(ctx, url, nil)
}

// GetH fetches with custom request headers. It satisfies the optional
// HeaderedFetcher interface the Executor uses when a recipe declares
// list.headers (e.g. an API auth/JWT/app-id header).
func (f *HTTPFetcher) GetH(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	return f.g.Get(ctx, url, headers)
}
