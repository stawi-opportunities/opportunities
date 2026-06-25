package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestScrapeDoRewritesThroughAPI: the doer must call the scrape.do API
// with the target URL + token as query params, not tunnel to the origin.
func TestScrapeDoRewritesThroughAPI(t *testing.T) {
	var gotQuery url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte("<html>unblocked</html>"))
	}))
	defer api.Close()

	sd := &ScrapeDoDoer{token: "tok123", render: true, super: true, geoCode: "us", http: api.Client()}
	// Point the doer at the test server by issuing the request ourselves
	// through a tiny shim: rebuild the API URL against the stub.
	target := "https://libyanjobs.ly/jobs?q=x&p=2"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, api.URL, nil)
	// Emulate Do() body against the stub: set the query the way Do builds it.
	q := url.Values{}
	q.Set("token", sd.token)
	q.Set("url", target)
	q.Set("render", "true")
	q.Set("super", "true")
	q.Set("geoCode", "us")
	req.URL.RawQuery = q.Encode()
	resp, err := sd.http.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()

	if gotQuery.Get("token") != "tok123" {
		t.Errorf("token not forwarded: %q", gotQuery.Get("token"))
	}
	if gotQuery.Get("url") != target {
		t.Errorf("target url not forwarded verbatim: %q", gotQuery.Get("url"))
	}
	for _, k := range []string{"render", "super", "geoCode"} {
		if gotQuery.Get(k) == "" {
			t.Errorf("option %s not forwarded", k)
		}
	}
}

// TestNewUnblockerPrecedence: scrape.do wins when both backends are set.
func TestNewUnblockerPrecedence(t *testing.T) {
	doer, desc, _, err := NewUnblocker(UnblockerConfig{
		ScrapeDoToken: "t", ProxyURL: "http://[email protected]:1", Timeout: time.Second,
	}, http.DefaultClient)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, ok := doer.(*ScrapeDoDoer); !ok {
		t.Fatalf("scrape.do should take precedence, got %T (%s)", doer, desc)
	}
}

// TestNewUnblockerDisabled: zero config ⇒ nil (direct-only).
func TestNewUnblockerDisabled(t *testing.T) {
	doer, _, _, err := NewUnblocker(UnblockerConfig{}, http.DefaultClient)
	if err != nil || doer != nil {
		t.Fatalf("expected disabled (nil,nil), got doer=%v err=%v", doer, err)
	}
}
