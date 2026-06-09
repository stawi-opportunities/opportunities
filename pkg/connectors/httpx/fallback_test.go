package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDoer struct {
	status int
	err    error
	body   string
	calls  int
}

func (d *fakeDoer) Do(_ *http.Request) (*http.Response, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return &http.Response{
		StatusCode: d.status,
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Header:     make(http.Header),
	}, nil
}

func newReq(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://site.test/jobs", nil)
	require.NoError(t, err)
	return r
}

func TestFallbackDoer_DirectSuccessSkipsUnblocker(t *testing.T) {
	direct := &fakeDoer{status: 200, body: "ok"}
	unb := &fakeDoer{status: 200, body: "via-proxy"}
	resp, err := NewFallbackDoer(direct, unb).Do(newReq(t))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, direct.calls)
	assert.Equal(t, 0, unb.calls, "unblocker must not be touched when direct succeeds")
}

func TestFallbackDoer_BlockedRetriesViaUnblocker(t *testing.T) {
	for _, status := range []int{403, 429, 451, 503} {
		direct := &fakeDoer{status: status, body: "blocked"}
		unb := &fakeDoer{status: 200, body: "via-proxy"}
		resp, err := NewFallbackDoer(direct, unb).Do(newReq(t))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		b, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "via-proxy", string(b), "status %d should be served via unblocker", status)
		assert.Equal(t, 1, unb.calls)
	}
}

func TestFallbackDoer_DirectErrorRetriesViaUnblocker(t *testing.T) {
	direct := &fakeDoer{err: fmt.Errorf("connection reset by peer")}
	unb := &fakeDoer{status: 200, body: "via-proxy"}
	resp, err := NewFallbackDoer(direct, unb).Do(newReq(t))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, unb.calls)
}

func TestFallbackDoer_NoUnblockerReturnsDirect(t *testing.T) {
	direct := &fakeDoer{status: 403, body: "blocked"}
	resp, err := NewFallbackDoer(direct, nil).Do(newReq(t))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, 1, direct.calls)
}

func TestNewProxyDoer(t *testing.T) {
	// No CA pinned → builds, but flags insecure.
	d, insecure, err := NewProxyDoer("http://user:pass@brd.superproxy.io:33335", "", 30*time.Second)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.True(t, insecure)

	// Bad CA PEM → error.
	_, _, err = NewProxyDoer("http://user:pass@brd.superproxy.io:33335", "not-a-pem", 30*time.Second)
	assert.Error(t, err)

	// Malformed / hostless URL → error.
	_, _, err = NewProxyDoer("://nope", "", 30*time.Second)
	assert.Error(t, err)
	_, _, err = NewProxyDoer("justtext", "", 30*time.Second)
	assert.Error(t, err)
}
