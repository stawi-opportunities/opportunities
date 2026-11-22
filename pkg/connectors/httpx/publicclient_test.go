package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestIsPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":         true,
		"104.16.0.1":      true,
		"2606:4700::1":    true,
		"127.0.0.1":       false,
		"10.1.2.3":        false,
		"172.16.5.5":      false,
		"192.168.1.1":     false,
		"169.254.169.254": false, // cloud metadata
		"100.64.0.1":      false, // CGNAT
		"0.0.0.0":         false,
		"0.1.2.3":         false,
		"224.0.0.1":       false,
		"::1":             false,
		"fd00::1":         false,
		"fe80::1":         false,
		"::ffff:10.0.0.1": false, // IPv4-mapped private
	}
	for ip, want := range cases {
		if got := isPublicAddr(netip.MustParseAddr(ip)); got != want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestPublicClient_RefusesLoopbackTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("internal secret"))
	}))
	defer srv.Close()

	resp, err := NewPublicClient(5 * time.Second).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("public client fetched a loopback URL")
	}
	if !errors.Is(err, ErrNonPublicAddress) {
		t.Fatalf("error = %v, want ErrNonPublicAddress", err)
	}
}

func TestPublicClient_RejectsNonHTTPRedirect(t *testing.T) {
	c := NewPublicClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodGet, "file:///etc/passwd", nil)
	if err := c.CheckRedirect(req, []*http.Request{{}}); err == nil {
		t.Fatal("redirect to file:// was allowed")
	}
	via := make([]*http.Request, maxPublicRedirects)
	ok, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if err := c.CheckRedirect(ok, via); err == nil {
		t.Fatal("redirect chain longer than the limit was allowed")
	}
}
