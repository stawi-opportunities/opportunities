package httpx

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// TestBrowserHeadersAreCoherent asserts the emulated request carries a
// full, self-consistent Chrome navigation header set — the thing
// anti-bot gateways score on.
func TestBrowserHeadersAreCoherent(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, "opportunities-bot/2.0")
	if _, _, err := c.Get(context.Background(), srv.URL, nil); err != nil {
		t.Fatalf("get: %v", err)
	}

	if ua := got.Get("User-Agent"); ua != browserUA {
		t.Fatalf("bot UA not replaced by browser UA: %q", ua)
	}
	for _, h := range []string{"Accept", "Accept-Language", "Accept-Encoding", "Sec-Ch-Ua", "Sec-Fetch-Mode"} {
		if got.Get(h) == "" {
			t.Errorf("missing browser header %s", h)
		}
	}
	// The Sec-CH-UA brand version must match the UA major version, or the
	// inconsistency itself flags the client.
	if !bytes.Contains([]byte(got.Get("Sec-Ch-Ua")), []byte("131")) {
		t.Errorf("Sec-Ch-Ua %q not coherent with UA 131", got.Get("Sec-Ch-Ua"))
	}
}

// TestCallerHeadersWin: a connector asking for JSON must override the
// browser default Accept.
func TestCallerHeadersWin(t *testing.T) {
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, "x")
	if _, _, err := c.Get(context.Background(), srv.URL, map[string]string{"Accept": "application/json"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if accept != "application/json" {
		t.Fatalf("caller Accept overridden: %q", accept)
	}
}

// TestPlainModeIdentifiesHonestly: emulation off ⇒ only the UA, no
// client hints.
func TestPlainModeIdentifiesHonestly(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, "opportunities-bot/2.0").PlainMode()
	if _, _, err := c.Get(context.Background(), srv.URL, nil); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Get("User-Agent") != "opportunities-bot/2.0" {
		t.Fatalf("plain mode UA = %q", got.Get("User-Agent"))
	}
	if got.Get("Sec-Ch-Ua") != "" {
		t.Fatalf("plain mode leaked client hints")
	}
}

// TestDecodeBodyRoundTrips covers every encoding the browser profile
// advertises — if we claim to accept it, we must decode it.
func TestDecodeBodyRoundTrips(t *testing.T) {
	payload := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 200)

	enc := func(name string, w io.WriteCloser, buf *bytes.Buffer) []byte {
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("%s write: %v", name, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("%s close: %v", name, err)
		}
		return buf.Bytes()
	}

	var gz, zl, br bytes.Buffer
	gzBytes := enc("gzip", gzip.NewWriter(&gz), &gz)
	zlBytes := enc("zlib", zlib.NewWriter(&zl), &zl)
	brBytes := enc("br", brotli.NewWriter(&br), &br)

	cases := map[string][]byte{
		"":        payload,
		"gzip":    gzBytes,
		"deflate": zlBytes,
		"br":      brBytes,
	}
	for encoding, raw := range cases {
		out, err := decodeBody(encoding, raw)
		if err != nil {
			t.Fatalf("decode %q: %v", encoding, err)
		}
		if !bytes.Equal(out, payload) {
			t.Fatalf("decode %q: round-trip mismatch (%d vs %d bytes)", encoding, len(out), len(payload))
		}
	}
}

// TestGetDecompressesBrotli: end-to-end — a brotli-encoded response is
// transparently decoded by Get.
func TestGetDecompressesBrotli(t *testing.T) {
	payload := []byte("<html><body>job listing</body></html>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		bw := brotli.NewWriter(&buf)
		_, _ = bw.Write(payload)
		_ = bw.Close()
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, "x")
	body, status, err := c.Get(context.Background(), srv.URL, nil)
	if err != nil || status != 200 {
		t.Fatalf("get: status=%d err=%v", status, err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("brotli body not decoded: %q", body)
	}
}
