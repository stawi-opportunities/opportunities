package sourceverify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/lib/pq"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// stubHTTP returns a fixed response for every request.
type stubHTTP struct {
	status int
	body   string
	err    error
	last   *http.Request
}

func (s *stubHTTP) Do(req *http.Request) (*http.Response, error) {
	s.last = req
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// fakeRobots implements RobotsChecker.
type fakeRobots struct {
	allowed bool
	err     error
}

func (f *fakeRobots) Allowed(_ context.Context, _, _ string) (bool, error) {
	return f.allowed, f.err
}

// stubConnector + stubIterator implement connectors.Connector for tests.
type stubConnector struct {
	t     domain.SourceType
	items []domain.ExternalOpportunity
	err   error
}

func (s *stubConnector) Type() domain.SourceType { return s.t }
func (s *stubConnector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return connectors.NewSinglePageIterator(s.items, []byte("{}"), 200, s.err)
}

// minimalRegistry returns a registry preloaded with a "job" kind. The
// kind is intentionally minimal so verify_test doesn't need a full YAML
// fixture — it only needs to satisfy Lookup + Verify.
func minimalRegistry(t *testing.T) *opportunity.Registry {
	t.Helper()
	yaml := `
kind: job
display_name: Job
issuing_entity_label: Company
url_prefix: jobs
universal_required: [title]
kind_required: []
kind_optional: []
extraction_prompt: ""
`
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/job.yaml", []byte(yaml), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	reg, err := opportunity.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return reg
}

func TestVerifier_Run(t *testing.T) {
	reg := minimalRegistry(t)
	connReg := connectors.NewRegistry()
	connReg.Register(&stubConnector{
		t: domain.SourceGenericHTML,
		items: []domain.ExternalOpportunity{
			{Title: "Senior Engineer", Kind: "job"},
		},
	})

	tests := []struct {
		name         string
		src          domain.Source
		http         *stubHTTP
		robots       RobotsChecker
		connectors   *connectors.Registry
		registry     *opportunity.Registry
		wantPass     bool
		wantURLValid bool
		wantBlock    bool
	}{
		{
			name: "happy path",
			src: domain.Source{
				BaseModel: domain.BaseModel{ID: "s1"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://example.com/jobs",
				Kinds:     pq.StringArray{"job"},
			},
			http:         &stubHTTP{status: 200, body: "ok"},
			robots:       &fakeRobots{allowed: true},
			connectors:   connReg,
			registry:     reg,
			wantPass:     true,
			wantURLValid: true,
			wantBlock:    true, // BlocklistClean true
		},
		{
			name: "blocked domain",
			src: domain.Source{
				BaseModel: domain.BaseModel{ID: "s2"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://linkedin.com/jobs",
				Kinds:     pq.StringArray{"job"},
			},
			http:         &stubHTTP{status: 200, body: "ok"},
			robots:       &fakeRobots{allowed: true},
			connectors:   connReg,
			registry:     reg,
			wantPass:     false,
			wantURLValid: true,
			wantBlock:    false,
		},
		{
			name: "unknown kind",
			src: domain.Source{
				BaseModel: domain.BaseModel{ID: "s3"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://example.com/grants",
				Kinds:     pq.StringArray{"funding"}, // not in our minimal registry
			},
			http:         &stubHTTP{status: 200, body: "ok"},
			robots:       &fakeRobots{allowed: true},
			connectors:   connReg,
			registry:     reg,
			wantPass:     false,
			wantURLValid: true,
			wantBlock:    true,
		},
		{
			name: "unreachable url",
			src: domain.Source{
				BaseModel: domain.BaseModel{ID: "s4"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://example.com",
				Kinds:     pq.StringArray{"job"},
			},
			http:         &stubHTTP{err: errors.New("dial tcp: connection refused")},
			robots:       &fakeRobots{allowed: true},
			connectors:   connReg,
			registry:     reg,
			wantPass:     false,
			wantURLValid: true,
			wantBlock:    true,
		},
		{
			name: "robots disallows",
			src: domain.Source{
				BaseModel: domain.BaseModel{ID: "s5"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://example.com/jobs",
				Kinds:     pq.StringArray{"job"},
			},
			http:         &stubHTTP{status: 200, body: "ok"},
			robots:       &fakeRobots{allowed: false},
			connectors:   connReg,
			registry:     reg,
			wantPass:     false,
			wantURLValid: true,
			wantBlock:    true,
		},
		{
			name: "invalid base url",
			src: domain.Source{
				BaseModel: domain.BaseModel{ID: "s6"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "ftp://nope/",
				Kinds:     pq.StringArray{"job"},
			},
			http:         &stubHTTP{status: 200, body: "ok"},
			robots:       &fakeRobots{allowed: true},
			connectors:   connReg,
			registry:     reg,
			wantPass:     false,
			wantURLValid: false,
			wantBlock:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVerifier(Config{
				HTTP:       tc.http,
				Connectors: tc.connectors,
				Registry:   tc.registry,
				Robots:     tc.robots,
			})
			rep := v.Run(context.Background(), &tc.src)
			if rep == nil {
				t.Fatalf("nil report")
			}
			if rep.OverallPass != tc.wantPass {
				j, _ := json.MarshalIndent(rep, "", "  ")
				t.Errorf("OverallPass=%v want %v\nreport:\n%s", rep.OverallPass, tc.wantPass, j)
			}
			if rep.URLValid != tc.wantURLValid {
				t.Errorf("URLValid=%v want %v", rep.URLValid, tc.wantURLValid)
			}
			if rep.BlocklistClean != tc.wantBlock {
				t.Errorf("BlocklistClean=%v want %v", rep.BlocklistClean, tc.wantBlock)
			}
			if rep.CompletedAt == nil {
				t.Errorf("CompletedAt is nil; verifier should always finalize")
			}
		})
	}
}

func TestVerifier_SampleExtraction(t *testing.T) {
	reg := minimalRegistry(t)
	connReg := connectors.NewRegistry()
	connReg.Register(&stubConnector{
		t: domain.SourceGenericHTML,
		items: []domain.ExternalOpportunity{
			{Title: "Hello", Kind: "job"},
		},
	})

	v := NewVerifier(Config{
		HTTP:       &stubHTTP{status: 200, body: "ok"},
		Connectors: connReg,
		Registry:   reg,
		Robots:     &fakeRobots{allowed: true},
	})
	src := &domain.Source{
		BaseModel: domain.BaseModel{ID: "s"},
		Type:      domain.SourceGenericHTML,
		BaseURL:   "https://example.com/jobs",
		Kinds:     pq.StringArray{"job"},
	}
	rep := v.Run(context.Background(), src)
	if !rep.SampleExtracted {
		t.Errorf("expected SampleExtracted=true")
	}
	if rep.SampleTitle != "Hello" {
		t.Errorf("SampleTitle=%q want %q", rep.SampleTitle, "Hello")
	}
}

func TestIsBlockedHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"linkedin.com", true},
		{"www.linkedin.com", true},
		{"jobs.linkedin.com", true},
		{"example.com", false},
		{"jobs.stawi.org", true},
		{"sub.pages.dev", true},
	}
	for _, tc := range tests {
		if got := IsBlockedHost(tc.host); got != tc.want {
			t.Errorf("IsBlockedHost(%q)=%v want %v", tc.host, got, tc.want)
		}
	}
}

func TestParseRobots(t *testing.T) {
	body := `User-agent: *
Disallow: /private
Allow: /private/public

User-agent: badbot
Disallow: /
`
	allowed := parseRobots(body, "stawi-bot/1.0").allowed
	if !allowed("/jobs") {
		t.Errorf("expected /jobs allowed under * group")
	}
	if allowed("/private/secret") {
		t.Errorf("/private/secret should be disallowed")
	}
	if !allowed("/private/public/x") {
		t.Errorf("/private/public/x should be allowed (longer match)")
	}

	bad := parseRobots(body, "badbot").allowed
	if bad("/anything") {
		t.Errorf("badbot should be disallowed everywhere")
	}
}
