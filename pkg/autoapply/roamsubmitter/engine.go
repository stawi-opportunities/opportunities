// Package roamsubmitter implements the autoapply.Submitter for ROAM
// Africa (Ringier) job boards — BrighterMonday (Kenya) and Jobberman
// (Nigeria) — which share one Laravel "Easy apply" platform.
//
// The apply form lives on the listing page (/listings/<slug>) with
// id="enquiry-form". Most fields are hidden and pre-filled server-side
// from the candidate's on-site profile; only the cover letter
// (`description`) is supplied per application. The submitter scrapes the
// live form, replays every value verbatim, overrides `description`,
// resolves the Alpine-rendered CV/currency selectors, and POSTs multipart
// to the form's action — then verifies the listing appears in the
// candidate's enquiries dashboard.
//
// All of that is identical across the sister sites. The ONLY things that
// differ are captured in Site (defined in sites.go):
//
//	name   — submitter identity / SubmitResult.Method
//	source — domain.SourceType handled
//	origin — scheme+host; everything else (CanHandle prefix, Origin
//	         header, enquiries-dashboard URL) derives from it
package roamsubmitter

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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pitabwire/util"
	"golang.org/x/net/html"

	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

const (
	enquiryFormID = "enquiry-form"
	// enquiriesPath is the candidate's "my applications" dashboard,
	// appended to a site's origin for the round-trip success check. Same
	// route across every ROAM board.
	enquiriesPath  = "/account/customer/enquiries"
	defaultTimeout = 30 * time.Second
)

// Submitter implements autoapply.Submitter for a single ROAM site.
type Submitter struct {
	site     Site
	sessions authsession.SessionProvider
	timeout  time.Duration
}

// Config bundles constructor arguments shared by every site.
type Config struct {
	Sessions    authsession.SessionProvider
	HTTPTimeout time.Duration
}

// New constructs a submitter for the given site.
func New(site Site, cfg Config) *Submitter {
	t := cfg.HTTPTimeout
	if t == 0 {
		t = defaultTimeout
	}
	return &Submitter{site: site, sessions: cfg.Sessions, timeout: t}
}

// Name implements autoapply.Submitter.
func (s *Submitter) Name() string { return s.site.name }

// CanHandle returns true for the site's source + any /listings/<slug>
// URL on its origin. Both ROAM sites embed the apply form on the listing
// page, so this rule is identical — only the origin differs.
func (s *Submitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType != s.site.source {
		return false
	}
	return strings.HasPrefix(applyURL, s.site.origin+"/listings/")
}

// Submit implements autoapply.Submitter.
//
// Flow:
//  1. Load the candidate's captured session from authsession.
//  2. Build an http.Client whose jar has those cookies.
//  3. GET the listing URL; bail session_expired on /login redirect or 401.
//  4. Parse <form id="enquiry-form">; bail form_changed if absent.
//  5. Replay every hidden input + selected <select> value, override
//     `description` with the cover letter, resolve the Alpine CV/currency
//     selectors the server leaves empty.
//  6. Profile-incomplete guard: any required field still empty → skip
//     profile_incomplete (the on-site profile is missing that datum).
//  7. POST multipart to the form action with Origin + Referer headers.
//  8. Confirm the listing now appears in the enquiries dashboard.
func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	log := util.Log(ctx).WithField("submitter", s.site.name).WithField("candidate_id", req.CandidateID)

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
	defer func() { _ = getResp.Body.Close() }()

	if loggedOut(getResp) {
		_ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
		log.Info("roam: detected logged-out on listing GET; revoked session")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_expired"}, nil
	}

	body, err := io.ReadAll(io.LimitReader(getResp.Body, 8<<20))
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("listing read: %w", err)
	}

	form, err := extractEnquiryForm(body)
	if err != nil {
		log.WithError(err).Warn("roam: enquiry form not found")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}

	// Override the cover letter with what the intent carries. The platform
	// requires `description` non-empty (required on the textarea).
	form.Fields["description"] = coverLetterOrFallback(req, form.Title)

	// Resolve the CV + currency controls the platform renders via Alpine,
	// and fill the per-application salary the on-site profile never stores,
	// from the candidate's Stawi salary expectation. Runs BEFORE the guard
	// so a satisfiable field isn't flagged as missing.
	finalizeFields(form, body, req)

	// Incomplete-profile guard: these apply forms pre-fill their required
	// fields from the candidate's on-site profile. Any still empty means
	// the profile is missing that datum — replaying empties would just
	// earn a validation rejection, so skip with a clear, actionable reason
	// (the handler turns this into a "complete your profile" CTA).
	if missing := form.missingRequired(); len(missing) > 0 {
		detail := strings.Join(missing, ",")
		log.WithField("missing_fields", detail).
			Warn("roam: required apply fields empty — candidate on-site profile incomplete")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "profile_incomplete", SkipDetail: detail}, nil
	}

	contentType, postBody, err := buildMultipart(form.Fields)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("build multipart: %w", err)
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, form.Action, postBody)
	if err != nil {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}
	postReq.Header.Set("Content-Type", contentType)
	postReq.Header.Set("Origin", s.site.origin)
	applyBrowserHeaders(postReq, sess, req.ApplyURL)

	postResp, err := client.Do(postReq)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("apply POST: %w", err)
	}
	defer func() { _ = postResp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(postResp.Body, 1<<20))

	log.WithField("post_status", postResp.StatusCode).
		WithField("post_url", form.Action).
		WithField("listing_id", form.ListingID).
		WithField("final_url", postResp.Request.URL.String()).
		Info("roam: POST complete")

	if postResp.StatusCode >= 400 {
		log.WithField("body_preview", previewBody(respBody)).Warn("roam: POST 4xx — rejecting")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}
	if loggedOut(postResp) {
		_ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "session_expired"}, nil
	}
	if looksLikeError(respBody) {
		log.WithField("body_preview", previewBody(respBody)).
			Warn("roam: 2xx response contained validation-error markers")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}

	// Ground-truth check: the platform can return 200 re-rendering the
	// listing (looks like success) even when the application was silently
	// rejected. The authoritative signal is the listing appearing in the
	// candidate's enquiries dashboard.
	slug := extractListingSlug(req.ApplyURL)
	if slug == "" {
		log.Warn("roam: no slug to verify; accepting on POST status only")
		return autoapply.SubmitResult{Method: s.site.name, ExternalRef: form.ListingID}, nil
	}

	confirmed, idxBody, cerr := s.confirmOnDashboard(ctx, client, sess, slug)
	if cerr != nil {
		// We couldn't verify — don't claim a success we can't see. Skip so
		// the message is retried rather than recorded as applied.
		log.WithError(cerr).Warn("roam: enquiries verification fetch failed — not confirming")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "verification_failed"}, nil
	}
	if !confirmed {
		log.WithField("slug", slug).
			WithField("post_body_preview", previewBody(respBody)).
			WithField("index_preview", previewBody(idxBody)).
			Warn("roam: POST returned 2xx/3xx but listing missing from enquiries dashboard — rejecting")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}

	log.WithField("slug", slug).Info("roam: round-trip verified — listing visible in enquiries dashboard")
	return autoapply.SubmitResult{Method: s.site.name, ExternalRef: form.ListingID}, nil
}

// confirmOnDashboard GETs the candidate's enquiries dashboard
// (origin + enquiriesPath) and checks the just-applied listing slug
// appears as a link. Returns (confirmed, raw body, error); network/HTTP
// errors come back as err so the caller can distinguish "couldn't verify"
// from "verified and absent".
func (s *Submitter) confirmOnDashboard(
	ctx context.Context,
	client *http.Client,
	sess *authsession.Session,
	slug string,
) (bool, []byte, error) {
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.site.origin+enquiriesPath, nil)
	if err != nil {
		return false, nil, err
	}
	applyBrowserHeaders(getReq, sess, "")

	resp, err := client.Do(getReq)
	if err != nil {
		return false, nil, fmt.Errorf("enquiries GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, nil, fmt.Errorf("enquiries GET: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return false, nil, err
	}
	// Slugs carry a short alphanumeric tail that won't collide, so a
	// substring match on the listing href is enough.
	return bytes.Contains(body, []byte("/listings/"+slug)), body, nil
}

// coverLetterOrFallback returns the intent's cover letter, or a safe
// generic letter when the matching pipeline didn't generate one (only a
// local-dev / smoke-test path; production always fills CoverLetter).
func coverLetterOrFallback(req autoapply.SubmitRequest, title string) string {
	if cl := strings.TrimSpace(req.CoverLetter); cl != "" {
		return cl
	}
	return fmt.Sprintf(
		"Hello,\n\nI am applying for the %s role and would welcome the opportunity to discuss how my experience fits.\n\nBest regards,\n%s",
		strings.TrimSpace(title), strings.TrimSpace(req.FullName),
	)
}

// extractListingSlug pulls the slug from a /listings/<slug> apply URL.
// Returns "" when the URL doesn't match the expected shape.
func extractListingSlug(applyURL string) string {
	const marker = "/listings/"
	i := strings.Index(applyURL, marker)
	if i < 0 {
		return ""
	}
	rest := applyURL[i+len(marker):]
	if j := strings.IndexAny(rest, "?#/"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// previewBody returns the first ~600 chars of a response body as a
// printable single line, for logging diagnostics on unexpected outcomes.
func previewBody(b []byte) string {
	const max = 600
	if len(b) > max {
		b = b[:max]
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// ──────────────────────────────────────────────────────────────────
// CV + currency resolution (the Alpine-rendered controls)
// ──────────────────────────────────────────────────────────────────

// currencySelectedRE captures the *selected* array literal from the
// Alpine `multiSelect(options, selected, …, 'currency_id', …)` call —
// e.g. the raw text `["116"]` (unicode-escaped quotes) or, in
// tests, `["116"]`. digitsRE then extracts the id from it.
var (
	currencySelectedRE = regexp.MustCompile(
		`multiSelect\(JSON\.parse\('[^']*'\),\s*JSON\.parse\('([^']*)'\),[^)]*?'currency_id'`)
	digitsRE = regexp.MustCompile(`\d+`)
)

// currencyDefault returns the pre-selected currency id, or "" when none.
// The id lives inside the selected array between escaped quotes; since
// each escaped quote renders as the literal digits "0022", we take the
// first digit run that isn't "0022".
func currencyDefault(body []byte) string {
	m := currencySelectedRE.FindSubmatch(body)
	if m == nil {
		return ""
	}
	for _, run := range digitsRE.FindAllString(string(m[1]), -1) {
		if run != "0022" {
			return run
		}
	}
	return ""
}

// finalizeFields fills the values the platform renders via Alpine (which
// the server HTML leaves empty even for a complete profile) plus the
// per-application salary the on-site profile never stores:
//
//   - CV: when an uploaded CV exists (uploaded_cv), select it the way the
//     browser does — current_cv=1 ("use existing CV") + resume_id=<cv>.
//   - currency: default currency_id to the form's Alpine-selected value
//     when the scraped <select> came back empty.
//   - salary_expectation: fill from the candidate's Stawi salary
//     expectation, since the source asks for it per application and never
//     pre-fills it from the on-site profile.
//
// Every step is a no-op when the field is already populated, so it is
// safe for every ROAM site regardless of profile state. A field left
// empty here (e.g. no salary on file) is caught by the missing-required
// guard and surfaced as profile_incomplete.
func finalizeFields(form *Form, body []byte, req autoapply.SubmitRequest) {
	if cv := form.Fields["uploaded_cv"]; cv != "" {
		if form.Fields["current_cv"] == "" {
			form.Fields["current_cv"] = "1" // 1 = use existing uploaded CV
		}
		if form.Fields["resume_id"] == "" {
			form.Fields["resume_id"] = cv
		}
	}
	if form.Fields["currency_id"] == "" {
		if cur := currencyDefault(body); cur != "" {
			form.Fields["currency_id"] = cur
		}
	}
	if form.Fields["salary_expectation"] == "" {
		if exp := salaryExpectation(req); exp != "" {
			form.Fields["salary_expectation"] = exp
		}
	}
}

// salaryExpectation renders the candidate's salary figure for the form,
// preferring the upper bound (what they're targeting). Returns "" when
// no salary is on file.
func salaryExpectation(req autoapply.SubmitRequest) string {
	v := req.SalaryMax
	if v <= 0 {
		v = req.SalaryMin
	}
	if v <= 0 {
		return ""
	}
	return strconv.Itoa(v)
}

// ──────────────────────────────────────────────────────────────────
// HTML form extraction
// ──────────────────────────────────────────────────────────────────

// Form is the parsed shape of a ROAM apply form. Fields preserves every
// replayed value; finalizeFields may mutate it before the POST.
type Form struct {
	Action    string
	ListingID string
	Title     string
	Fields    map[string]string
	// required is the set of field names the page marked required, used by
	// missingRequired for the incomplete-profile guard.
	required map[string]bool
}

// missingRequired lists required fields whose value is empty at POST time
// (after the cover-letter override + finalizeFields). `description` is
// always set, so it never appears here.
func (f *Form) missingRequired() []string {
	var out []string
	for name := range f.required {
		if strings.TrimSpace(f.Fields[name]) == "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// extractEnquiryForm walks the parsed HTML for the enquiry form. Returns
// an error when the form is missing — caller surfaces that as form_changed.
func extractEnquiryForm(body []byte) (*Form, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	out := &Form{Fields: map[string]string{}, required: map[string]bool{}}
	out.Title = findTitle(doc)

	formNode := findFormByID(doc, enquiryFormID)
	if formNode == nil {
		return nil, errors.New("enquiry-form not found")
	}
	out.Action = attrValue(formNode, "action")
	if out.Action == "" {
		return nil, errors.New("enquiry-form has no action")
	}

	walkElements(formNode, func(n *html.Node) {
		name := attrValue(n, "name")
		if name == "" {
			return
		}
		var value string
		switch n.Data {
		case "input":
			// Skip file inputs; the CV is replayed via the resume
			// reference / current_cv selector, never re-uploaded here.
			if attrValue(n, "type") == "file" {
				return
			}
			value = attrValue(n, "value")
		case "textarea":
			value = textContent(n)
		case "select":
			value = selectedOptionValue(n)
		default:
			return
		}
		out.Fields[name] = value
		if isRequiredField(n) {
			out.required[name] = true
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

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

// isRequiredField reports whether a form control is required. The ROAM
// forms mark this either with a bare/valued `required` attribute or with
// "required" inside the Parsley `data-rules` list (the Alpine selects).
func isRequiredField(n *html.Node) bool {
	return hasAttr(n, "required") ||
		strings.Contains(attrValue(n, "data-rules"), "required")
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
			if hasAttr(x, "selected") {
				selected = v
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
			// Don't auto-follow into /login — let the caller treat that as
			// session_expired.
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

// applyBrowserHeaders sets the headers a real browser would send for a
// same-origin navigation. Mirrors the captured Chrome request.
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

// looksLikeError checks a 2xx response body for known validation-error
// markers. Laravel apps often return 200 with an error page rather than
// 422; this is a coarse guard against that.
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
// field. No file uploads — the CV is replayed via a resume reference to
// the candidate's existing profile CV.
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
