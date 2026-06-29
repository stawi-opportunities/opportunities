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

// methodMarker anchors the "Method of Application" section. MyJobMag
// renders it as a heading; we match case-insensitively on the text.
const methodMarker = "method of application"

// emailRE matches a plain email address. Used to pull the recipient out
// of the Method of Application prose.
var emailRE = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

// Submitter implements autoapply.Submitter for MyJobMag listings.
type Submitter struct {
	sender autoapply.EmailSender
	client *http.Client
}

// Config bundles the constructor arguments.
type Config struct {
	Sender autoapply.EmailSender
	// HTTPTimeout caps the listing GET. Default 30s.
	HTTPTimeout time.Duration
}

// New constructs a MyJobMag submitter.
func New(cfg Config) *Submitter {
	t := cfg.HTTPTimeout
	if t == 0 {
		t = defaultTimeout
	}
	return &Submitter{sender: cfg.Sender, client: &http.Client{Timeout: t}}
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
// Flow: GET the public listing → parse the Method of Application block for
// an email + the job title → queue the application email via the sender.
// No email in the block ⇒ skip "no_email" (the listing applies off-site).
func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	log := util.Log(ctx).WithField("submitter", s.Name()).WithField("candidate_id", req.CandidateID)

	if s.sender == nil || !s.sender.Configured() {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_sender"}, nil
	}
	if strings.TrimSpace(req.ApplyURL) == "" {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_apply_url"}, nil
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.ApplyURL, nil)
	if err != nil {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_apply_url"}, nil
	}
	getReq.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	getReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := s.client.Do(getReq)
	if err != nil {
		// Transient — propagate so the queue redelivers.
		return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: listing GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		log.WithField("status", resp.StatusCode).Warn("myjobmag: listing GET non-2xx")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "listing_unavailable"}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: listing read: %w", err)
	}

	email, title := parseMethodOfApplication(body)
	if email == "" {
		log.Debug("myjobmag: no application email in Method of Application; skipping")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_email"}, nil
	}

	// MyJobMag listings typically ask for the position as the email
	// subject; use the scraped job title, falling back to a generic
	// subject inside the sender when empty.
	if err := s.sender.Send(ctx, email, title, req); err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: send: %w", err)
	}

	log.WithField("recipient", email).WithField("subject", title).Info("myjobmag: application email queued")
	return autoapply.SubmitResult{Method: s.Name()}, nil
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
