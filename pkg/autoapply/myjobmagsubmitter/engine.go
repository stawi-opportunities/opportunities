// Package myjobmagsubmitter implements the autoapply.Submitter for the
// MyJobMag job-board family. Editions: myjobmag.com (Nigeria / flagship),
// myjobmag.co.ke (Kenya), myjobmag.co.za (South Africa), myjobmagghana.com
// (Ghana — note the different domain shape) and myjobmag.co.uk (UK).
//
// Unlike the ROAM boards, MyJobMag is NOT a logged-in "easy apply"
// platform — its listing pages are public server-rendered HTML and most
// jobs are applied to OFF the platform. Each listing carries a "Method of
// Application" block; when that block names an email address, applicants
// are expected to forward their CV there. That is the only case this
// submitter handles: it GETs the public listing (no session, no browser),
// scrapes the application email + the job title, and queues an application
// email (CV by reference) through the shared EmailSender. Listings that
// route applications anywhere else (external link, on-site form only) are
// skipped with reason "no_email" so they are never falsely recorded as
// applied.
package myjobmagsubmitter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pitabwire/util"
	"golang.org/x/net/html"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

const defaultTimeout = 30 * time.Second

// userAgent mirrors a real Chrome navigation so MyJobMag serves the full
// server-rendered page (and the apply form) rather than a bot-stripped one.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"

// methodMarker anchors the "Method of Application" section. MyJobMag
// renders it as a heading; we match case-insensitively on the text.
const methodMarker = "method of application"

// emailRE matches a plain email address. Used to pull the recipient out
// of the Method of Application prose.
var emailRE = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

// RecaptchaSolver solves the reCAPTCHA v2 checkbox on the on-site
// /job-application form from its site key + page URL, returning the
// g-recaptcha-response token to replay in the POST. Satisfied by
// captcha.TwoCaptcha (a proxyless 2captcha HTTP client) — no browser. The
// interface is declared here, at the point of use, so this submitter stays
// pure-HTTP and depends on neither the browser nor the captcha package.
type RecaptchaSolver interface {
	SolveRecaptchaV2(ctx context.Context, siteKey, pageURL string) (token string, err error)
}

// Submitter implements autoapply.Submitter for MyJobMag listings.
type Submitter struct {
	sender  autoapply.EmailSender
	captcha RecaptchaSolver
	timeout time.Duration
}

// Config bundles the constructor arguments.
type Config struct {
	// Sender delivers the email path (Method of Application names an email).
	Sender autoapply.EmailSender
	// Captcha solves the reCAPTCHA on the on-site /job-application form.
	// When nil, listings that only offer the on-site form are skipped.
	Captcha RecaptchaSolver
	// HTTPTimeout caps each GET/POST. Default 30s.
	HTTPTimeout time.Duration
}

// New constructs a MyJobMag submitter.
func New(cfg Config) *Submitter {
	t := cfg.HTTPTimeout
	if t == 0 {
		t = defaultTimeout
	}
	return &Submitter{sender: cfg.Sender, captcha: cfg.Captcha, timeout: t}
}

// httpClient builds a per-Submit client with its own cookie jar so the
// PHPSESSID seeded by the form GET persists into the POST. A fresh jar per
// call avoids leaking cookies across candidates.
func (s *Submitter) httpClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: s.timeout, Jar: jar}
}

// Name implements autoapply.Submitter.
func (s *Submitter) Name() string { return "myjobmag_email" }

// CanHandle claims MyJobMag listings across every edition. The flow is
// stateless, so one submitter covers the whole family — we only need the
// source type and a MyJobMag host. The editions don't share one domain
// shape: most are myjobmag.<tld> (myjobmag.com — Nigeria/flagship,
// myjobmag.co.ke — Kenya, myjobmag.co.za — South Africa, myjobmag.co.uk —
// UK) but Ghana is myjobmagghana.com, so we match the "myjobmag" stem
// rather than a "myjobmag." prefix.
func (s *Submitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType != domain.SourceMyJobMag {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(applyURL))
	if err != nil {
		return false
	}
	return isMyJobMagHost(u.Hostname())
}

// isMyJobMagHost reports whether host belongs to a MyJobMag edition.
// Matches the "myjobmag" stem so it covers both myjobmag.<tld> and the
// odd-one-out myjobmagghana.com.
func isMyJobMagHost(host string) bool {
	return strings.Contains(strings.ToLower(host), "myjobmag")
}

// Submit implements autoapply.Submitter.
//
// Flow: GET the apply URL, then route by how the listing accepts
// applications:
//   - Method of Application names an email ⇒ queue the application email
//     via the sender (CV by reference).
//   - otherwise an on-site /job-application/<id> form ⇒ scrape it, solve
//     its reCAPTCHA via 2captcha, and POST the application (CV uploaded).
//   - neither ⇒ skip "no_email" (applies off-site, e.g. external link).
func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	log := util.Log(ctx).WithField("submitter", s.Name()).WithField("candidate_id", req.CandidateID)

	if strings.TrimSpace(req.ApplyURL) == "" {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_apply_url"}, nil
	}

	// One client+jar for the whole flow so the PHPSESSID from this GET
	// carries into a later form POST on the same edition.
	client := s.httpClient()
	body, status, err := s.get(ctx, client, req.ApplyURL, "")
	if err != nil {
		// Transient — propagate so the queue redelivers.
		return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: listing GET: %w", err)
	}
	if status >= 400 {
		log.WithField("status", status).Warn("myjobmag: listing GET non-2xx")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "listing_unavailable"}, nil
	}

	// Email path takes priority when the listing names an address.
	email, title := parseMethodOfApplication(body)
	if email != "" {
		if s.sender == nil || !s.sender.Configured() {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_sender"}, nil
		}
		// MyJobMag listings typically ask for the position as the email
		// subject; use the scraped job title (sender falls back to a
		// generic subject when empty).
		if err := s.sender.Send(ctx, email, title, req); err != nil {
			return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: send: %w", err)
		}
		log.WithField("recipient", email).WithField("subject", title).Info("myjobmag: application email queued")
		return autoapply.SubmitResult{Method: s.Name()}, nil
	}

	// No email — try the on-site application form.
	formURL := findApplyFormURL(body, req.ApplyURL)
	if formURL == "" {
		log.Debug("myjobmag: no application email and no on-site form; skipping")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_email"}, nil
	}
	return s.submitViaForm(ctx, client, formURL, body, req)
}

// get fetches url with browser-like headers and returns the (limited) body
// and status code. referer is set when non-empty.
func (s *Submitter) get(ctx context.Context, client *http.Client, url, referer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// parseMethodOfApplication extracts the application email and the job
// title from a listing page. The email is taken from the "Method of
// Application" section only — addresses elsewhere on the page (and any on
// a myjobmag.* domain, which are the site's own) are ignored. Returns an
// empty email when the section is absent or names no external address.
func parseMethodOfApplication(body []byte) (email, title string) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", ""
	}
	title = findTitle(doc)

	text := renderText(doc)
	lower := strings.ToLower(text)
	idx := strings.Index(lower, methodMarker)
	if idx < 0 {
		return "", title
	}
	section := text[idx:]

	for _, candidate := range emailRE.FindAllString(section, -1) {
		addr := strings.Trim(candidate, ".,;:)")
		// Skip the site's own addresses (support@, info@myjobmag…) across
		// every edition, including @myjobmagghana.com.
		if isMyJobMagHost(addr) {
			continue
		}
		return addr, title
	}
	return "", title
}

// findTitle returns the first non-empty <h1> text — the job title on a
// MyJobMag listing page.
func findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "h1" {
		if t := strings.TrimSpace(textContent(n)); t != "" {
			return t
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findTitle(c); got != "" {
			return got
		}
	}
	return ""
}

// renderText flattens the document to whitespace-collapsed text so the
// Method of Application marker and the email survive regardless of the
// surrounding markup.
func renderText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style") {
			return
		}
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
			sb.WriteString(" ")
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
