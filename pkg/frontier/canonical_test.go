package frontier_test

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/frontier"
)

func TestCanonicalizeURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "lowercases scheme and host",
			in:   "HTTPS://Example.COM/Jobs/123",
			want: "https://example.com/Jobs/123",
		},
		{
			name: "strips trailing slash on path",
			in:   "https://example.com/jobs/123/",
			want: "https://example.com/jobs/123",
		},
		{
			name: "keeps root slash",
			in:   "https://example.com/",
			want: "https://example.com/",
		},
		{
			name: "removes fragment",
			in:   "https://example.com/jobs/123#apply",
			want: "https://example.com/jobs/123",
		},
		{
			name: "strips utm and tracking params",
			in:   "https://example.com/jobs/123?utm_source=x&utm_campaign=y&fbclid=z&gclid=w&ref=a&source=b",
			want: "https://example.com/jobs/123",
		},
		{
			name: "sorts remaining query params",
			in:   "https://example.com/jobs?b=2&a=1&c=3",
			want: "https://example.com/jobs?a=1&b=2&c=3",
		},
		{
			name: "strips tracking but keeps real params, sorted",
			in:   "https://example.com/jobs?utm_medium=email&z=last&a=first",
			want: "https://example.com/jobs?a=first&z=last",
		},
		{
			name: "combined normalization",
			in:   "HTTP://Example.com/jobs/123/?utm_source=x&q=go#frag",
			want: "http://example.com/jobs/123?q=go",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
		{
			name: "unparseable falls back to raw",
			in:   "not a url",
			want: "not a url",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := frontier.CanonicalizeURL(tc.in)
			if got != tc.want {
				t.Fatalf("CanonicalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
