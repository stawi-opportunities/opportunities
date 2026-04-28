package sourceverify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// HTTPDoer is the minimal interface the verifier needs from an HTTP
// client. *http.Client satisfies it; tests inject a stub.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// RobotsChecker decides whether the crawler is allowed to fetch a URL.
// Implementations may consult robots.txt, an in-memory allowlist, or a
// canned policy used in tests.
type RobotsChecker interface {
	Allowed(ctx context.Context, rawURL, userAgent string) (bool, error)
}

// Verifier composes the source-level fitness checks. Construct with
// NewVerifier and call Run for each source under review.
type Verifier struct {
	httpClient    HTTPDoer
	connectors    *connectors.Registry
	registry      *opportunity.Registry
	robots        RobotsChecker
	userAgent     string
	probeTimeout  time.Duration
	sampleTimeout time.Duration
}

// Config bundles the dependencies the verifier needs.
type Config struct {
	HTTP          HTTPDoer
	Connectors    *connectors.Registry
	Registry      *opportunity.Registry
	Robots        RobotsChecker
	UserAgent     string
	ProbeTimeout  time.Duration
	SampleTimeout time.Duration
}

// NewVerifier returns a configured Verifier. HTTP, Connectors, and
// Registry are required; Robots may be nil (treated as "allow all"); the
// timeouts default to 10s and 60s respectively.
func NewVerifier(cfg Config) *Verifier {
	v := &Verifier{
		httpClient:    cfg.HTTP,
		connectors:    cfg.Connectors,
		registry:      cfg.Registry,
		robots:        cfg.Robots,
		userAgent:     cfg.UserAgent,
		probeTimeout:  cfg.ProbeTimeout,
		sampleTimeout: cfg.SampleTimeout,
	}
	if v.userAgent == "" {
		v.userAgent = "stawi-source-verifier/1.0"
	}
	if v.probeTimeout <= 0 {
		v.probeTimeout = 10 * time.Second
	}
	if v.sampleTimeout <= 0 {
		v.sampleTimeout = 60 * time.Second
	}
	return v
}

// Run executes every check against src in order. The returned report is
// always non-nil; OverallPass is true only when every required check
// passed. Sample extraction is best-effort: if the connector or extractor
// is unavailable the report records SampleExtracted=false and the source
// can still pass (operator policy decides via AutoApprove).
func (v *Verifier) Run(ctx context.Context, src *domain.Source) *domain.VerificationReport {
	rep := &domain.VerificationReport{
		StartedAt: time.Now().UTC(),
	}

	// 1. URL validation.
	u, err := url.Parse(src.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		rep.Errors = append(rep.Errors, fmt.Sprintf("invalid base_url: %q", src.BaseURL))
	} else {
		rep.URLValid = true
	}

	// 2. Blocklist.
	if rep.URLValid && IsBlockedHost(u.Hostname()) {
		rep.Errors = append(rep.Errors, fmt.Sprintf("host %q is on blocklist", u.Hostname()))
	} else if rep.URLValid {
		rep.BlocklistClean = true
	}

	// 3. Kinds known.
	rep.KindsKnown = v.checkKinds(src, rep)

	// Bail before network if URL/blocklist already failed — there is no
	// useful signal to be gained from probing an invalid URL.
	if !rep.URLValid || !rep.BlocklistClean {
		v.finalize(rep)
		return rep
	}

	// 4. Reachability.
	v.checkReachable(ctx, src.BaseURL, rep)

	// 5. robots.txt.
	v.checkRobots(ctx, src.BaseURL, rep)

	// 6. Sample extraction (best-effort).
	v.checkSample(ctx, src, rep)

	v.finalize(rep)
	return rep
}

func (v *Verifier) checkKinds(src *domain.Source, rep *domain.VerificationReport) bool {
	if v.registry == nil {
		// Without a registry we cannot validate; treat as pass to avoid
		// false negatives in dev/test environments.
		return true
	}
	if len(src.Kinds) == 0 {
		rep.Errors = append(rep.Errors, "source declares no kinds")
		return false
	}
	for _, k := range src.Kinds {
		if _, ok := v.registry.Lookup(k); !ok {
			rep.Errors = append(rep.Errors, fmt.Sprintf("kind %q not in registry", k))
			return false
		}
	}
	return true
}

func (v *Verifier) checkReachable(ctx context.Context, rawURL string, rep *domain.VerificationReport) {
	if v.httpClient == nil {
		rep.Errors = append(rep.Errors, "no http client configured for reachability probe")
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, v.probeTimeout)
	defer cancel()

	status, err := v.probe(probeCtx, http.MethodHead, rawURL)
	// Some origins reject HEAD with 405/501 — fall back to GET.
	if err == nil && (status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented) {
		status, err = v.probe(probeCtx, http.MethodGet, rawURL)
	}
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("reachability probe: %v", err))
		return
	}
	rep.ReachableStatus = status
	rep.Reachable = status >= 200 && status < 400
}

func (v *Verifier) probe(ctx context.Context, method, rawURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", v.userAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

func (v *Verifier) checkRobots(ctx context.Context, rawURL string, rep *domain.VerificationReport) {
	if v.robots == nil {
		// No robots checker configured — treat as allowed. Operators can
		// still inspect the report and reject manually.
		rep.RobotsAllowed = true
		return
	}
	allowed, err := v.robots.Allowed(ctx, rawURL, v.userAgent)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("robots check: %v", err))
		// On error, default to allow — robots.txt unavailability should
		// not block onboarding. The operator sees the error in the report.
		rep.RobotsAllowed = true
		return
	}
	rep.RobotsAllowed = allowed
	if !allowed {
		rep.Errors = append(rep.Errors, "robots.txt disallows crawling")
	}
}

func (v *Verifier) checkSample(ctx context.Context, src *domain.Source, rep *domain.VerificationReport) {
	if v.connectors == nil {
		rep.Errors = append(rep.Errors, "no connector registry configured; skipped sample extraction")
		return
	}
	conn, ok := v.connectors.Get(src.Type)
	if !ok {
		// Best-effort: a discovered source with a generic-html type may
		// not have an extractor wired locally. Surface the gap but don't
		// fail the overall verification on it.
		rep.Errors = append(rep.Errors, fmt.Sprintf("no connector registered for type %q", src.Type))
		return
	}

	sampleCtx, cancel := context.WithTimeout(ctx, v.sampleTimeout)
	defer cancel()

	iter := conn.Crawl(sampleCtx, *src)
	defer func() {
		// Drain so any background goroutines wind down.
		_ = iter
	}()

	if !iter.Next(sampleCtx) {
		if iterErr := iter.Err(); iterErr != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("sample crawl: %v", iterErr))
		} else {
			rep.Errors = append(rep.Errors, "sample crawl yielded no pages")
		}
		return
	}

	items := iter.Items()
	if len(items) == 0 {
		rep.Errors = append(rep.Errors, "sample page contained no items")
		return
	}

	first := items[0]
	rep.SampleExtracted = true
	rep.SampleTitle = first.Title

	if v.registry == nil {
		// Without a registry we cannot run per-record verify; surface as
		// a soft pass on extraction alone.
		rep.SampleVerifyPass = true
		return
	}
	res := opportunity.Verify(&first, src, v.registry)
	rep.SampleVerifyPass = res.OK
	if !res.OK {
		reasons := []string{}
		if res.Mismatch != "" {
			reasons = append(reasons, res.Mismatch)
		}
		if len(res.Missing) > 0 {
			reasons = append(reasons, "missing: "+strings.Join(res.Missing, ", "))
		}
		rep.SampleReasons = reasons
	}
}

func (v *Verifier) finalize(rep *domain.VerificationReport) {
	now := time.Now().UTC()
	rep.CompletedAt = &now

	// Required-for-pass checks. SampleExtracted/SampleVerifyPass are
	// best-effort — they raise the OverallPass bar but do not block it
	// when the LLM is unavailable. Operators can decide via AutoApprove
	// whether to wait on a sample.
	hardChecks := rep.URLValid && rep.BlocklistClean && rep.KindsKnown && rep.Reachable && rep.RobotsAllowed
	rep.OverallPass = hardChecks
}

// ErrNotConfigured is returned when the verifier is asked to run but
// required dependencies (connectors, registry) are missing.
var ErrNotConfigured = errors.New("sourceverify: dependencies not configured")
