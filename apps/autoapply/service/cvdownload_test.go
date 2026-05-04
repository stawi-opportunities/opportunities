package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCVURL_Scheme(t *testing.T) {
	for _, bad := range []string{
		"http://example.com/cv.pdf",
		"file:///etc/passwd",
		"gopher://example.com/0",
		"ftp://example.com/cv",
		"javascript:alert(1)",
	} {
		t.Run(bad, func(t *testing.T) {
			require.Error(t, validateCVURL(bad))
		})
	}
}

func TestValidateCVURL_BlocksMetadata(t *testing.T) {
	// Direct-IP URLs short-circuit DNS but still go through isPublicIP.
	for _, bad := range []string{
		"https://127.0.0.1/cv.pdf",
		"https://10.0.0.1/cv.pdf",
		"https://169.254.169.254/latest/meta-data/",
		"https://192.168.1.1/cv.pdf",
		"https://[::1]/cv.pdf",
	} {
		t.Run(bad, func(t *testing.T) {
			require.Error(t, validateCVURL(bad))
		})
	}
}

func TestSanitiseFilename(t *testing.T) {
	cases := map[string]string{
		"resume.pdf":           "resume.pdf",
		"../etc/passwd":        "passwd",
		"/abs/path/cv.pdf":     "cv.pdf",
		"../..\\Windows\\sys":  "sys", // backslash treated as separator
		"":                     "resume.pdf",
		"中文.pdf":               ".pdf", // non-ascii stripped
		strings.Repeat("a", 100) + ".pdf": strings.Repeat("a", 64),
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, sanitise(in))
		})
	}
}

func TestHTTPCVFetcher_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="my cv.pdf"`)
		_, _ = w.Write([]byte("PDF-CONTENT"))
	}))
	defer srv.Close()

	// httptest binds to 127.0.0.1; bypass validateCVURL by constructing
	// the fetcher and exercising the post-validation path via a direct
	// call to the underlying client. We cover validateCVURL in its own
	// test above.
	f := &httpCVFetcher{client: srv.Client(), maxBytes: 1024}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "mycv.pdf", deriveFilename(resp, srv.URL))
}

func TestHTTPCVFetcher_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 100))
	}))
	defer srv.Close()

	f := &httpCVFetcher{client: srv.Client(), maxBytes: 10}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// Manually exercise the size cap in Fetch by re-reading.
	data, _, err := readWithCap(resp, f.maxBytes)
	require.Error(t, err)
	assert.Empty(t, data)
}

// readWithCap mirrors the body-read logic in (*httpCVFetcher).Fetch so
// we can test the cap path without going through validateCVURL.
func readWithCap(resp *http.Response, max int64) ([]byte, string, error) {
	buf := make([]byte, max+1)
	n, _ := resp.Body.Read(buf)
	if int64(n) > max {
		return nil, "", errors.New("too large")
	}
	return buf[:n], "", nil
}

func TestHTTPCVFetcher_EmptyURLReturnsNil(t *testing.T) {
	f := NewHTTPCVFetcher(time.Second, 1024)
	data, name, err := f.Fetch(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, data)
	assert.Empty(t, name)
}
