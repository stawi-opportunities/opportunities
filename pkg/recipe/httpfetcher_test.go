package recipe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubGetter struct{ c *http.Client }

func (s stubGetter) Get(ctx context.Context, url string, _ map[string]string) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := s.c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var b []byte
	tmp := make([]byte, 1024)
	for {
		n, rerr := resp.Body.Read(tmp)
		b = append(b, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return b, resp.StatusCode, nil
}

func TestHTTPFetcher_AdaptsHeaderedGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	f := NewHTTPFetcher(stubGetter{c: srv.Client()})
	body, status, err := f.Get(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.Equal(t, "hello", string(body))
}
