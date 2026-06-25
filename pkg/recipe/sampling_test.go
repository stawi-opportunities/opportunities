package recipe

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSampleFetcher struct{ pages map[string]string }

func (f fakeSampleFetcher) Get(_ context.Context, u string) ([]byte, int, error) {
	if p, ok := f.pages[u]; ok {
		return []byte(p), 200, nil
	}
	return nil, 404, fmt.Errorf("not found")
}

func TestDiscoverDetailURLs(t *testing.T) {
	listing := `<html><body>
	  <a href="/jobs/software-engineer-nairobi-abc123">SWE</a>
	  <a href="/jobs/accountant-kampala-99812">Accountant</a>
	  <a href="/jobs/">All jobs</a>
	  <a href="/about">About</a>
	  <a href="https://other.site/jobs/x-12345">offsite</a>
	  <a href="/listings/sous-chef-job-k7mqn5">Chef</a>
	</body></html>`
	f := fakeSampleFetcher{pages: map[string]string{"https://board.example/jobs": listing}}

	urls, err := DiscoverDetailURLs(context.Background(), f, "https://board.example/jobs", 2)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"https://board.example/jobs/software-engineer-nairobi-abc123",
		"https://board.example/jobs/accountant-kampala-99812",
	}, urls, "same-host detail-like links only, capped at n")

	_, err = DiscoverDetailURLs(context.Background(), f, "https://board.example/empty", 2)
	assert.Error(t, err, "fetch failure surfaces")

	f.pages["https://board.example/empty"] = `<html><a href="/about">About</a></html>`
	_, err = DiscoverDetailURLs(context.Background(), f, "https://board.example/empty", 2)
	assert.Error(t, err, "no detail-like links -> error")
}
