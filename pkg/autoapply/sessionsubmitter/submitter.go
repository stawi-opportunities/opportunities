// Package sessionsubmitter implements the autoapply.Submitter that
// replays an application using a session captured by the Stawi browser
// extension.
//
// The submitter is HTTP-only in Phase 5 — `apply_flow.type: http_form`
// in the source manifest. JS-heavy sources that need a headless
// browser are deferred to Phase 8 under
// pkg/autoapply/sessionsubmitter/headless.
//
// Flow:
//
//  1. Look up the manifest for req.SourceType. If auth_method !=
//     extension or apply_flow.type != http_form, refuse to handle.
//  2. Load the session via authsession.SessionProvider. Map
//     ErrSessionRequired/ErrSessionExpired to Method="skipped",
//     SkipReason="session_required"/"session_expired".
//  3. Build an http.Client with a cookie jar pre-seeded from the
//     session; copy User-Agent and Accept-Language from the captured
//     headers so the fingerprint matches what BrighterMonday saw at
//     login.
//  4. GET the apply URL — parse the response for the CSRF token if
//     declared, detect 3xx-to-login as a session-expired signal.
//  5. POST a multipart form built from the manifest's field map +
//     the SubmitRequest fields, then classify the response as
//     submitted / captcha / form_changed.
//
// Detected expiry triggers two side effects: Revoke() on the session
// provider so the next lookup returns ErrSessionRequired, and the
// caller (autoapply handler) emits SessionExpiredV1.
package sessionsubmitter

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

	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SkipReason values surfaced to the handler (and through it, to
// telemetry + UI). Keep low-cardinality.
const (
	ReasonSessionRequired = "session_required"
	ReasonSessionExpired  = "session_expired"
	ReasonFormChanged     = "form_changed"
	ReasonCaptcha         = "captcha"
	ReasonUnsupportedFlow = "unsupported_flow"
	ReasonNoApplyURL      = "no_apply_url"
)

// Submitter is the autoapply.Submitter registered ahead of the
// generic browser/email fallbacks.
type Submitter struct {
	manifests ManifestStore
	sessions  authsession.SessionProvider
	client    *http.Client
}

// ManifestStore is the slice of authmanifest.Store the submitter needs.
type ManifestStore interface {
	Lookup(sourceType domain.SourceType) (*authmanifest.Manifest, bool)
}

// Config bundles the constructor arguments.
type Config struct {
	Manifests ManifestStore
	Sessions  authsession.SessionProvider
	// HTTPTimeout caps each GET + POST. Default 30s.
	HTTPTimeout time.Duration
}

// New constructs a Submitter.
func New(cfg Config) *Submitter {
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Submitter{
		manifests: cfg.Manifests,
		sessions:  cfg.Sessions,
		client:    &http.Client{Timeout: timeout},
	}
}

// Name implements autoapply.Submitter.
func (s *Submitter) Name() string { return "session_replay" }

// CanHandle returns true when the source has an extension manifest
// with http_form apply flow and the URL matches the manifest's
// form_url_pattern.
func (s *Submitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	m, ok := s.manifests.Lookup(sourceType)
	if !ok {
		return false
	}
	if m.AuthMethod != authmanifest.AuthExtension {
		return false
	}
	if m.ApplyFlow.Type != authmanifest.ApplyHTTPForm {
		return false
	}
	return m.MatchesApplyURL(applyURL)
}

// Submit implements autoapply.Submitter.
func (s *Submitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	log := util.Log(ctx).
		WithField("submitter", s.Name()).
		WithField("source_type", req.SourceType).
		WithField("candidate_id", req.CandidateID)

	if req.ApplyURL == "" {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonNoApplyURL}, nil
	}
	m, ok := s.manifests.Lookup(req.SourceType)
	if !ok || m.ApplyFlow.Type != authmanifest.ApplyHTTPForm {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonUnsupportedFlow}, nil
	}

	sess, err := s.sessions.Session(ctx, req.CandidateID, req.SourceType)
	if errors.Is(err, authsession.ErrSessionRequired) {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonSessionRequired}, nil
	}
	if errors.Is(err, authsession.ErrSessionExpired) {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonSessionExpired}, nil
	}
	if err != nil {
		// Transient — propagate so the queue redelivers.
		return autoapply.SubmitResult{}, fmt.Errorf("session lookup: %w", err)
	}

	client, err := s.clientFromSession(sess, m)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("session-bound client: %w", err)
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.ApplyURL, nil)
	if err != nil {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonNoApplyURL}, nil
	}
	applySessionHeaders(getReq, sess)

	getResp, err := client.Do(getReq)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("apply GET: %w", err)
	}
	defer getResp.Body.Close()

	if loggedOut := looksLoggedOut(getResp, m); loggedOut {
		_ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
		log.WithField("status", getResp.StatusCode).Info("session_replay: detected logged-out on GET; revoked session")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonSessionExpired}, nil
	}

	getBody, err := io.ReadAll(io.LimitReader(getResp.Body, 4<<20))
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("apply GET read: %w", err)
	}

	csrfToken, csrfHeader := extractCSRF(m, client, getBody, req.ApplyURL)

	contentType, body, err := buildMultipart(m, req, csrfToken)
	if err != nil {
		log.WithError(err).Warn("session_replay: build multipart")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonFormChanged}, nil
	}

	postURL := postURLFromForm(getBody, req.ApplyURL)

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, body)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("apply POST build: %w", err)
	}
	postReq.Header.Set("Content-Type", contentType)
	postReq.Header.Set("Referer", req.ApplyURL)
	applySessionHeaders(postReq, sess)
	if csrfHeader != "" {
		postReq.Header.Set(csrfHeader, csrfToken)
	}

	postResp, err := client.Do(postReq)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("apply POST: %w", err)
	}
	defer postResp.Body.Close()

	if looksLoggedOut(postResp, m) {
		_ = s.sessions.Revoke(ctx, req.CandidateID, req.SourceType)
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonSessionExpired}, nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(postResp.Body, 1<<20))
	if isCaptcha(respBody) {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonCaptcha}, nil
	}
	if postResp.StatusCode >= 400 {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: ReasonFormChanged}, nil
	}

	return autoapply.SubmitResult{Method: "session_replay"}, nil
}

// ── internals ───────────────────────────────────────────────────────

func (s *Submitter) clientFromSession(sess *authsession.Session, m *authmanifest.Manifest) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	// Group cookies by their effective URL so the jar's SetCookies
	// can apply them correctly.
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
		host := std.Domain
		byHost[host] = append(byHost[host], std)
	}
	for host, cookies := range byHost {
		u := &url.URL{Scheme: "https", Host: host, Path: "/"}
		jar.SetCookies(u, cookies)
	}
	return &http.Client{
		Timeout: s.client.Timeout,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			// Stop and return the redirect itself when the destination
			// looks like the source's login page — looksLoggedOut will
			// then convert that into ReasonSessionExpired instead of
			// the client silently following into the login HTML.
			if m.LoginURL != "" {
				dest := req.URL.String()
				if strings.HasPrefix(dest, m.LoginURL) || strings.Contains(dest, "/login") {
					return http.ErrUseLastResponse
				}
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

func applySessionHeaders(r *http.Request, sess *authsession.Session) {
	if ua, ok := sess.Payload.Headers["User-Agent"]; ok && ua != "" {
		r.Header.Set("User-Agent", ua)
	}
	if lang, ok := sess.Payload.Headers["Accept-Language"]; ok && lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	if r.Header.Get("Accept") == "" {
		r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	}
}

// looksLoggedOut treats the response as a logged-out signal when:
//   - status is 401
//   - status is a 3xx redirect targeting the manifest's login_url
//
// This is intentionally conservative — false positives revoke the
// session and force a re-pair, which is high friction. False negatives
// just look like form_changed.
func looksLoggedOut(resp *http.Response, m *authmanifest.Manifest) bool {
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" && m.LoginURL != "" {
			return strings.HasPrefix(loc, m.LoginURL) || strings.Contains(loc, "/login")
		}
	}
	return false
}

// isCaptcha is a light heuristic — substring match for well-known
// challenge providers. Per-source manifest could declare its own
// markers in a later iteration.
func isCaptcha(body []byte) bool {
	lower := bytes.ToLower(body)
	markers := [][]byte{
		[]byte("g-recaptcha"),
		[]byte("h-captcha"),
		[]byte("cf-challenge"),
		[]byte("/recaptcha/api.js"),
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

// extractCSRF pulls the CSRF token + the header name to set on the
// POST. cookie-based CSRF reads from the jar; form_token reads a
// hidden input from the GET response body.
func extractCSRF(m *authmanifest.Manifest, client *http.Client, getBody []byte, applyURL string) (token, headerName string) {
	if m.ApplyFlow.CSRF == nil {
		return "", ""
	}
	c := m.ApplyFlow.CSRF
	switch c.Source {
	case "cookie":
		if c.Cookie == "" || c.Header == "" {
			return "", ""
		}
		u, err := url.Parse(applyURL)
		if err != nil || client.Jar == nil {
			return "", ""
		}
		for _, ck := range client.Jar.Cookies(u) {
			if ck.Name == c.Cookie {
				return ck.Value, c.Header
			}
		}
	case "form_token":
		if c.Field == "" {
			return "", ""
		}
		if val, ok := findInputValue(getBody, c.Field); ok {
			return val, "" // sent as a form field, not a header
		}
	}
	return "", ""
}

// findInputValue walks the parsed HTML for the first <input name="want">
// and returns its value attribute. Used for hidden CSRF inputs and
// other token-bearing fields.
func findInputValue(body []byte, want string) (string, bool) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", false
	}
	var (
		found string
		ok    bool
	)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if ok {
			return
		}
		if n.Type == html.ElementNode && n.Data == "input" {
			name, value := "", ""
			for _, a := range n.Attr {
				switch a.Key {
				case "name":
					name = a.Val
				case "value":
					value = a.Val
				}
			}
			if name == want {
				found = value
				ok = true
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found, ok
}

// postURLFromForm parses the <form action="..."> on the apply page so
// we POST to the right URL. Falls back to the apply URL itself when
// no form is found, which is the BrighterMonday default.
func postURLFromForm(getBody []byte, applyURL string) string {
	doc, err := html.Parse(bytes.NewReader(getBody))
	if err != nil {
		return applyURL
	}
	var (
		action string
		found  bool
	)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.ElementNode && n.Data == "form" {
			for _, a := range n.Attr {
				if a.Key == "action" {
					action = a.Val
					found = true
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if !found || action == "" {
		return applyURL
	}
	base, err := url.Parse(applyURL)
	if err != nil {
		return applyURL
	}
	ref, err := url.Parse(action)
	if err != nil {
		return applyURL
	}
	return base.ResolveReference(ref).String()
}

// buildMultipart constructs the application form body. For each field
// declared in the manifest, look up the SubmitRequest value via the
// field's `source` tag and write it to the multipart writer. The CV
// file (source=cv_bytes) gets a file part; everything else is a text
// part. If a manifest field's value resolves to empty, the field is
// skipped — many sources accept partial forms and reject only on
// genuine validation failures.
func buildMultipart(m *authmanifest.Manifest, req autoapply.SubmitRequest, csrfFormToken string) (contentType string, body io.Reader, err error) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)

	// Optional CSRF form-token field, if the manifest declared
	// form_token rather than a cookie-mirrored header.
	if m.ApplyFlow.CSRF != nil && m.ApplyFlow.CSRF.Source == "form_token" && csrfFormToken != "" {
		if err := w.WriteField(m.ApplyFlow.CSRF.Field, csrfFormToken); err != nil {
			return "", nil, err
		}
	}

	for _, field := range m.ApplyFlow.Fields {
		if field.Name == "" {
			continue
		}
		switch field.Source {
		case "cv_bytes":
			if len(req.CVBytes) == 0 {
				continue
			}
			name := req.CVFilename
			if name == "" {
				name = "resume.pdf"
			}
			fw, err := w.CreateFormFile(field.Name, name)
			if err != nil {
				return "", nil, err
			}
			if _, err := fw.Write(req.CVBytes); err != nil {
				return "", nil, err
			}
		case "cover_letter":
			if req.CoverLetter == "" {
				continue
			}
			if err := w.WriteField(field.Name, req.CoverLetter); err != nil {
				return "", nil, err
			}
		case "phone":
			if req.Phone == "" {
				continue
			}
			if err := w.WriteField(field.Name, req.Phone); err != nil {
				return "", nil, err
			}
		case "email":
			if req.Email == "" {
				continue
			}
			if err := w.WriteField(field.Name, req.Email); err != nil {
				return "", nil, err
			}
		case "full_name":
			if req.FullName == "" {
				continue
			}
			if err := w.WriteField(field.Name, req.FullName); err != nil {
				return "", nil, err
			}
		case "location":
			if req.Location == "" {
				continue
			}
			if err := w.WriteField(field.Name, req.Location); err != nil {
				return "", nil, err
			}
		case "current_title":
			if req.CurrentTitle == "" {
				continue
			}
			if err := w.WriteField(field.Name, req.CurrentTitle); err != nil {
				return "", nil, err
			}
		case "skills":
			if req.Skills == "" {
				continue
			}
			if err := w.WriteField(field.Name, req.Skills); err != nil {
				return "", nil, err
			}
		default:
			// Unknown source — skip silently rather than fail the
			// apply; misconfigured manifests are operational issues,
			// not user-facing errors.
		}
	}

	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return w.FormDataContentType(), buf, nil
}
