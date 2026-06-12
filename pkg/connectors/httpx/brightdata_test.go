package httpx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseBrightDataURL(t *testing.T) {
	key, zone, country, err := parseBrightDataURL("brightdata-api://72c7d3ce-02f6-4f3e-b2a7-43f254660005@web_unlocker1?country=ae")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if key != "72c7d3ce-02f6-4f3e-b2a7-43f254660005" || zone != "web_unlocker1" || country != "ae" {
		t.Fatalf("got key=%q zone=%q country=%q", key, zone, country)
	}
	if _, _, _, err := parseBrightDataURL("brightdata-api://web_unlocker1"); err == nil {
		t.Fatal("expected error when token missing")
	}
}

func TestIsBrightDataURL(t *testing.T) {
	if !isBrightDataURL("brightdata-api://k@z") {
		t.Fatal("should match")
	}
	if isBrightDataURL("http://brd-customer-x-zone-y:p@brd.superproxy.io:33335") {
		t.Fatal("plain proxy must not match")
	}
}

func TestBrightDataDoer_Do(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte("<html>unblocked job page</html>"))
	}))
	defer srv.Close()

	// Point the doer's API endpoint at the test server by overriding via a
	// custom transport: NewBrightDataDoer hits brightDataAPI, so we instead
	// exercise Do() against the test server through a rewriting client.
	d := NewBrightDataDoer("tok123", "web_unlocker1", "ae", rewriteClient(srv.URL), 5*time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://www.example.com/jobs", nil)
	resp, err := d.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<html>unblocked job page</html>" {
		t.Fatalf("body=%q", body)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("auth=%q", gotAuth)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["zone"] != "web_unlocker1" || payload["url"] != "https://www.example.com/jobs" ||
		payload["format"] != "raw" || payload["country"] != "ae" || payload["method"] != "GET" {
		t.Fatalf("payload wrong: %v", payload)
	}
}

// rewriteClient returns an HTTPDoer that redirects any request to base,
// preserving method + body, so the test exercises Do() without hitting the
// real Bright Data endpoint.
func rewriteClient(base string) HTTPDoer {
	return doerFunc(func(req *http.Request) (*http.Response, error) {
		u, _ := req.URL.Parse(base)
		req.URL = u
		req.Host = u.Host
		return http.DefaultClient.Do(req)
	})
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
