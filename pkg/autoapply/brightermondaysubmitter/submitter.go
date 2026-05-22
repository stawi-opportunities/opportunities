// Package brightermondaysubmitter implements the autoapply.Submitter
// for BrighterMonday's "Easy apply" flow.
//
// BrighterMonday's apply is a classic Laravel form POST:
//
//   POST /account/customer/enquiries/<listing_id>/store-enquiry
//   Content-Type: multipart/form-data
//   Cookies: laravel_session, XSRF-TOKEN
//
// The form lives on the listing page itself with id="enquiry-form".
// Most fields are hidden and pre-populated from the candidate's BM
// profile (salary expectation, currency, experience length, resume).
// Only the cover letter (`description`) is candidate-supplied per
// application.
//
// Our strategy is profile-conservative: we scrape the live form for
// every hidden / pre-selected value and replay them verbatim, then
// override `description` with the candidate's cover letter from the
// intent. That means any new hidden field BM adds (anti-bot tokens,
// new profile data) flows through automatically without a code change.
package brightermondaysubmitter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/pitabwire/util"
	"golang.org/x/net/html"

	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

const (
	enquiryFormID    = "enquiry-form"
	bmOrigin         = "https://www.brightermonday.co.ke"
	enquiriesIndexURL = "https://www.brightermonday.co.ke/account/customer/enquiries"
	defaultTimeout   = 30 * time.Second
)

// Submitter implements autoapply.Submitter for brightermonday.co.ke.
type Submitter struct {
	sessions authsession.SessionProvider
	timeout  time.Duration
}

// Config bundles constructor arguments.
type Config struct {
	Sessions    authsession.SessionProvider
	HTTPTimeout time.Duration
}

// New constructs the submitter.
func New(cfg Config) *Submitter {
	t := cfg.HTTPTimeout
	if t == 0 {
		t = defaultTimeout
	}
	return &Submitter{sessions: cfg.Sessions, timeout: t}
}

// Name implements autoapply.Submitter.
func (s *Submitter) Name() string { return "brightermonday" }

// CanHandle returns true for brightermonday source + any /listings/<slug>
// URL on the BM domain.
func (s *Submitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType != domain.SourceBrighterMonday {
		return false
	}
	return strings.HasPrefix(applyURL, bmOrigin+"/listings/")
}

// Submit implements autoapply.Submitter.
//
// Flow:
//   1. Load the candidate's captured BM session from authsession.
//   2. Build an http.Client whose jar has those cookies.
//   3. GET the listing URL; bail with session_expired if BM redirects
//      to /login or returns 401.
//   4. Parse the response HTML for <form id="enquiry-form">; bail
//      with form_changed if absent.
//   5. Replay every <input type=hidden> + the selected <select>
//      option value as fields, overriding `description` with the
//      candidate's cover letter.
//   6. POST multipart to the form's action attribute with Origin +
//      Referer headers matching what a real browser sends.
//   7. 2xx or 302 = submitted; 4xx = form_changed (BM rejected).
func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	log := util.Log(ctx).WithField("submitter", s.Name()).WithField("candidate_id", req.CandidateID)

	sess, err := s.sessions.Session(ctx, req.CandidateID, req.SourceType)
	if errors.Is(err, authsession.ErrSessionRequired) {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_required"}, nil
	}
	if errors.Is(err, authsession.ErrSessionExpired) {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_expired"}, nil
	}
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("session lookup: %w", err)
	}

	client, err := newClientFromSession(s.timeout, sess)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("session-bound client: %w", err)
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.ApplyURL, nil)
	if err != nil {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_apply_url"}, nil
	}
	applyBrowserHeaders(getReq, sess, "")

	getResp, err := client.Do(getReq)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("listing GET: %w", err)
	}
	defer getResp.Body.Close()

	if loggedOut(getResp) {
		_ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
		log.Info("brightermonday: detected logged-out on listing GET; revoked session")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_expired"}, nil
	}

	body, err := io.ReadAll(io.LimitReader(getResp.Body, 8<<20))
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("listing read: %w", err)
	}

	form, err := extractEnquiryForm(body)
	if err != nil {
		log.WithError(err).Warn("brightermonday: enquiry form not found")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}

	// Override cover letter with what the intent carries. BM requires
	// `description` to be non-empty (required="required" on the
	// textarea).
	coverLetter := strings.TrimSpace(req.CoverLetter)
	if coverLetter == "" {
		// A safe-and-generic fallback so the apply still goes through
		// for autoapply runs where the matching pipeline didn't
		// generate a tailored cover letter. The Stawi pipeline always
		// fills CoverLetter in production via the LLM, so this is
		// only a local-dev / smoke-test fallback.
		coverLetter = fmt.Sprintf(
			"Hello,\n\nI am applying for the %s role and would welcome the opportunity to discuss how my experience fits.\n\nBest regards,\n%s",
			strings.TrimSpace(form.Title), strings.TrimSpace(req.FullName),
		)
	}
	form.Fields["description"] = coverLetter

	// `valid_from` is BM's honeypot — leaving it empty (the form
	// default) is what a real user does; pre-filling it would
	// flag the submission as spam.

	contentType, postBody, err := buildMultipart(form.Fields)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("build multipart: %w", err)
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, form.Action, postBody)
	if err != nil {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}
	postReq.Header.Set("Content-Type", contentType)
	postReq.Header.Set("Origin", bmOrigin)
	applyBrowserHeaders(postReq, sess, req.ApplyURL)

	postResp, err := client.Do(postReq)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("apply POST: %w", err)
	}
	defer postResp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(postResp.Body, 1<<20))

	log.WithField("post_status", postResp.StatusCode).
		WithField("post_url", form.Action).
		WithField("listing_id", form.ListingID).
		WithField("final_url", postResp.Request.URL.String()).
		Info("brightermonday: POST complete")

	// Fast-path failures.
	if postResp.StatusCode >= 400 {
		log.WithField("body_preview", previewBody(respBody)).Warn("brightermonday: POST 4xx — rejecting")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}
	if loggedOut(postResp) {
		_ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_expired"}, nil
	}
	if looksLikeError(respBody) {
		log.WithField("body_preview", previewBody(respBody)).
			Warn("brightermonday: 2xx response contained validation-error markers")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}

	// Ground-truth check: BM can return 200 with a redirect-to-listing
	// page (looks like success) when the application was silently
	// rejected (e.g. profile incomplete, already applied, anti-spam).
	// The only authoritative signal is whether the listing now appears
	// in the candidate's /account/customer/enquiries index page. We
	// hit that page right after the POST and grep for the listing's
	// slug. The slug is extracted from the apply URL the intent
	// carries — that's what BM renders as the enquiry link.
	slug := extractListingSlug(req.ApplyURL)
	if slug == "" {
		// No slug to verify against — fall back to the soft-success
		// heuristic and warn so operators can spot the gap.
		log.Warn("brightermonday: no slug to verify; using POST status only")
		return autoapply.SubmitResult{Method: "brightermonday", ExternalRef: form.ListingID}, nil
	}

	confirmed, idxBody, idxErr := s.verifyOnEnquiriesIndex(ctx, client, sess, slug)
	if idxErr != nil {
		log.WithError(idxErr).Warn("brightermonday: enquiries verification fetch failed")
		// Verification fetch failed — we don't know whether the apply
		// landed. Treat as form_changed so we don't lie about it.
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "verification_failed"}, nil
	}
	if !confirmed {
		log.WithField("slug", slug).
			WithField("post_body_preview", previewBody(respBody)).
			WithField("index_preview", previewBody(idxBody)).
			Warn("brightermonday: POST returned 2xx/3xx but listing missing from enquiries index — rejecting")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}

	log.WithField("slug", slug).Info("brightermonday: round-trip verified — listing visible in enquiries index")
	return autoapply.SubmitResult{Method: "brightermonday", ExternalRef: form.ListingID}, nil
}

// verifyOnEnquiriesIndex GETs the candidate's enquiries dashboard and
// checks whether the just-submitted listing slug appears as a link.
// BM renders successful applications as `<a href=".../listings/<slug>">`
// entries in that page. Returns (confirmed, raw body, error).
//
// Network/HTTP errors are returned via the error so the caller can
// distinguish "we couldn't verify" from "we verified and it's missing".
func (s *Submitter) verifyOnEnquiriesIndex(
	ctx context.Context,
	client *http.Client,
	sess *authsession.Session,
	slug string,
) (bool, []byte, error) {
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, enquiriesIndexURL, nil)
	if err != nil {
		return false, nil, err
	}
	applyBrowserHeaders(getReq, sess, "")

	resp, err := client.Do(getReq)
	if err != nil {
		return false, nil, fmt.Errorf("enquiries GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Logged out, redirect, server error — we can't verify.
		return false, nil, fmt.Errorf("enquiries GET: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return false, nil, err
	}
	// Look for the slug appearing in a listing href. Substring is fine
	// because slugs are unique-enough — BM's job slugs end with a
	// short alphanumeric tail (e.g. "-7jvp7z") that won't collide.
	needle := []byte("/listings/" + slug)
	return bytes.Contains(body, needle), body, nil
}

// extractListingSlug pulls the slug from a BM apply URL.
//   https://www.brightermonday.co.ke/listings/oracle-apps-dba-45jq74
//                                              └────────slug────────┘
// Returns "" when the URL doesn't match the expected shape.
func extractListingSlug(applyURL string) string {
	const marker = "/listings/"
	i := strings.Index(applyURL, marker)
	if i < 0 {
		return ""
	}
	rest := applyURL[i+len(marker):]
	// Trim query/fragment.
	if j := strings.IndexAny(rest, "?#/"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// previewBody returns the first ~600 chars of a response body as a
// printable string, for logging diagnostics on unexpected outcomes.
func previewBody(b []byte) string {
	const max = 600
	if len(b) > max {
		b = b[:max]
	}
	// Strip newlines so it fits on one log line.
	return strings.Join(strings.Fields(string(b)), " ")
}

// ──────────────────────────────────────────────────────────────────
// HTML form extraction
// ──────────────────────────────────────────────────────────────────

// enquiryForm is the parsed shape of BM's apply form. Fields preserves
// hidden input values verbatim so the submitter can replay them.
type enquiryForm struct {
	Action    string
	ListingID string
	Title     string
	Fields    map[string]string
}

// extractEnquiryForm walks the parsed HTML for the BM enquiry form.
// Returns an error when the form is missing — caller surfaces that as
// `form_changed`.
func extractEnquiryForm(body []byte) (*enquiryForm, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	out := &enquiryForm{Fields: map[string]string{}}

	// Pull title for the fallback cover letter.
	out.Title = findTitle(doc)

	formNode := findFormByID(doc, enquiryFormID)
	if formNode == nil {
		return nil, errors.New("enquiry-form not found")
	}
	out.Action = attrValue(formNode, "action")
	if out.Action == "" {
		return nil, errors.New("enquiry-form has no action")
	}

	// Collect every input/textarea/select inside the form.
	walkElements(formNode, func(n *html.Node) {
		switch n.Data {
		case "input":
			name := attrValue(n, "name")
			if name == "" {
				return
			}
			// Skip the file input (`uploaded_cv` is also a <select>;
			// type="file" inputs are nameless or have a different
			// name pattern); we never upload a fresh CV here, we use
			// the resume_id that points at the user's existing one.
			if attrValue(n, "type") == "file" {
				return
			}
			out.Fields[name] = attrValue(n, "value")
		case "textarea":
			name := attrValue(n, "name")
			if name == "" {
				return
			}
			// Textarea values usually come from inner text; for
			// BM's `description` we override anyway, so leave empty
			// unless inner text exists.
			out.Fields[name] = textContent(n)
		case "select":
			name := attrValue(n, "name")
			if name == "" {
				return
			}
			out.Fields[name] = selectedOptionValue(n)
		}
	})

	out.ListingID = out.Fields["listing_id"]
	return out, nil
}

func findFormByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "id") == id {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findFormByID(c, id); got != nil {
			return got
		}
	}
	return nil
}

func findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "h1" {
		t := strings.TrimSpace(textContent(n))
		if t != "" {
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

func walkElements(root *html.Node, fn func(*html.Node)) {
	if root.Type == html.ElementNode {
		fn(root)
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		walkElements(c, fn)
	}
}

func attrValue(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
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

func selectedOptionValue(sel *html.Node) string {
	var first, selected string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && x.Data == "option" {
			v := attrValue(x, "value")
			if first == "" {
				first = v
			}
			for _, a := range x.Attr {
				if a.Key == "selected" {
					selected = v
				}
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(sel)
	if selected != "" {
		return selected
	}
	return first
}

// ──────────────────────────────────────────────────────────────────
// HTTP helpers
// ──────────────────────────────────────────────────────────────────

func newClientFromSession(timeout time.Duration, sess *authsession.Session) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	byHost := map[string][]*http.Cookie{}
	for _, c := range sess.Payload.Cookies {
		std := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   strings.TrimPrefix(c.Domain, "."),
			Path:     defaultPath(c.Path),
			HttpOnly: c.HTTPOnly,
			Secure:   c.Secure,
		}
		if c.Expires != nil {
			std.Expires = *c.Expires
		}
		byHost[std.Domain] = append(byHost[std.Domain], std)
	}
	for host, cookies := range byHost {
		u := &url.URL{Scheme: "https", Host: host, Path: "/"}
		jar.SetCookies(u, cookies)
	}
	return &http.Client{
		Timeout: timeout,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			// Don't auto-follow into /login — let the caller treat
			// that as session_expired.
			if strings.Contains(req.URL.Path, "/login") {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}, nil
}

func defaultPath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// applyBrowserHeaders sets the headers a real browser would send for
// a same-origin navigation. Mirrors the cURL Chrome captured.
func applyBrowserHeaders(r *http.Request, sess *authsession.Session, referer string) {
	if ua, ok := sess.Payload.Headers["User-Agent"]; ok && ua != "" {
		r.Header.Set("User-Agent", ua)
	} else {
		r.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	}
	if lang, ok := sess.Payload.Headers["Accept-Language"]; ok && lang != "" {
		r.Header.Set("Accept-Language", lang)
	} else {
		r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	}
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
}

func loggedOut(resp *http.Response) bool {
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		return loc != "" && (strings.Contains(loc, "/login") || strings.Contains(loc, "/signin"))
	}
	return false
}

// looksLikeError checks the 2xx response body for known error
// indicators. Laravel apps often return 200 with a validation-error
// page rather than 422; this is a coarse guard against that.
func looksLikeError(body []byte) bool {
	lower := bytes.ToLower(body)
	markers := [][]byte{
		[]byte("the description field is required"),
		[]byte("validation.failed"),
		[]byte("the cv field is required"),
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

// buildMultipart writes every (name, value) pair as a multipart form
// field. No file uploads — BM's apply uses `resume_id` to reference
// the candidate's existing profile CV via the dropdown.
func buildMultipart(fields map[string]string) (contentType string, body io.Reader, err error) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return w.FormDataContentType(), buf, nil
}
