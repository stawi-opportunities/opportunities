package myjobmagsubmitter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
)

const maxCVFileSize = "2097152" // 2 MiB, mirrors the form's MAX_FILE_SIZE

// debugResponseSink, when non-nil, receives the raw apply POST response
// body. Wired only by live/diagnostic tests to capture MyJobMag's real
// success/error page; nil (no-op) in production.
var debugResponseSink func([]byte)

var (
	// sitekeyRE pulls the reCAPTCHA v2 site key from the apply page.
	sitekeyRE = regexp.MustCompile(`data-sitekey="([^"]+)"`)
	// jobAppPathRE recognises the on-site apply form URL and captures the
	// numeric job id used as the `position` field.
	jobAppPathRE = regexp.MustCompile(`/job-application/(\d+)`)
	// applyFormActionRE captures the action of the apply <form>. MyJobMag
	// uses action="" (post-to-self), so an empty capture is expected.
	applyFormActionRE = regexp.MustCompile(`(?is)<form[^>]*id="d-apply-form"[^>]*action="([^"]*)"`)
	// optionRE matches a numeric-id <option> inside a <select>.
	optionRE = regexp.MustCompile(`(?is)<option\s+value="(\d+)"\s*>([^<]*)</option>`)
)

// findApplyFormURL returns the absolute URL of the on-site apply form for
// a listing, or "" when the listing offers no on-site form (truly
// off-site). If pageURL is already the form page it is returned as-is;
// otherwise the listing's /job-application/<id> link is resolved.
func findApplyFormURL(body []byte, pageURL string) string {
	if jobAppPathRE.MatchString(pageURL) {
		return pageURL
	}
	m := jobAppPathRE.FindString(string(body))
	if m == "" {
		return ""
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(m)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

// submitViaForm scrapes the on-site application form, solves its
// reCAPTCHA via the configured solver, and POSTs the application with the
// CV uploaded. listingBody is the already-fetched body at req.ApplyURL,
// reused when it is itself the form page.
func (s *Submitter) submitViaForm(
	ctx context.Context,
	client *http.Client,
	formURL string,
	listingBody []byte,
	req autoapply.SubmitRequest,
) (autoapply.SubmitResult, error) {
	log := util.Log(ctx).WithField("submitter", s.Name()).WithField("candidate_id", req.CandidateID)

	if s.captcha == nil {
		log.Debug("myjobmag: on-site form but no captcha solver configured; skipping")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "captcha_unavailable"}, nil
	}
	if len(req.CVBytes) == 0 {
		// The on-site form uploads the CV file; without it the application
		// is pointless. (The email path carries the CV by reference, but
		// this path needs the bytes.)
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "no_cv"}, nil
	}

	// Fetch the form page unless req.ApplyURL already was it.
	formBody := listingBody
	if formURL != req.ApplyURL {
		body, status, err := s.get(ctx, client, formURL, req.ApplyURL)
		if err != nil {
			return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: form GET: %w", err)
		}
		if status >= 400 {
			log.WithField("status", status).Warn("myjobmag: form GET non-2xx")
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "listing_unavailable"}, nil
		}
		formBody = body
	}

	siteKey := firstSubmatch(sitekeyRE, formBody)
	if siteKey == "" {
		log.Warn("myjobmag: apply form has no reCAPTCHA site key")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}
	positionID := firstSubmatch(jobAppPathRE, []byte(formURL))
	if positionID == "" {
		positionID = firstSubmatch(jobAppPathRE, formBody)
	}

	// Resolve the required numeric location id from the candidate's
	// preferred location against the form's own option list.
	locID := matchLocationID(locationOptions(formBody), req.Location)
	if locID == "" {
		log.WithField("candidate_location", req.Location).
			Warn("myjobmag: could not map candidate location to a form option")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "location_unmatched"}, nil
	}

	token, err := s.captcha.SolveRecaptchaV2(ctx, siteKey, formURL)
	if err != nil {
		// Solver failures are transient (timeouts, service blips) — let the
		// queue redeliver rather than recording a false skip.
		return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: captcha solve: %w", err)
	}

	contentType, postBody, err := buildFormMultipart(positionID, locID, token, req)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: build multipart: %w", err)
	}

	action := resolveAction(formBody, formURL)
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, action, postBody)
	if err != nil {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}
	postReq.Header.Set("Content-Type", contentType)
	postReq.Header.Set("User-Agent", userAgent)
	postReq.Header.Set("Referer", formURL)
	if origin := originOf(formURL); origin != "" {
		postReq.Header.Set("Origin", origin)
	}

	resp, err := client.Do(postReq)
	if err != nil {
		return autoapply.SubmitResult{}, fmt.Errorf("myjobmag: apply POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if debugResponseSink != nil {
		debugResponseSink(respBody)
	}

	log.WithField("post_status", resp.StatusCode).WithField("position", positionID).
		WithField("final_url", resp.Request.URL.String()).
		WithField("body_preview", previewBody(respBody)).
		Info("myjobmag: apply POST complete")

	if resp.StatusCode >= 400 {
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}
	// MyJobMag returns 200 and re-renders a page for BOTH a duplicate and a
	// validation failure, so status alone can't confirm a submission.
	if alreadyApplied(respBody) {
		log.Info("myjobmag: listing reports the candidate already applied")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "already_applied"}, nil
	}
	if looksLikeFormError(respBody) {
		log.Warn("myjobmag: apply POST returned a validation/captcha error")
		return autoapply.SubmitResult{Method: "skipped", SkipReason: "form_changed"}, nil
	}

	return autoapply.SubmitResult{Method: "myjobmag_form", ExternalRef: positionID}, nil
}

// buildFormMultipart assembles the application POST body, mirroring the
// fields the live form submits. sender_experience is omitted (the form
// does not require it and the request carries no experience figure).
func buildFormMultipart(positionID, locationID, captchaToken string, req autoapply.SubmitRequest) (string, *bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)

	fields := [][2]string{
		{"position", positionID},
		{"sender_name", req.FullName},
		{"sender_email", req.Email},
		{"sender_phone", req.Phone},
		{"sender_location", locationID},
		{"apply_subject", ""},
		{"apply_body", coverLetterOrFallback(req)},
		{"MAX_FILE_SIZE", maxCVFileSize},
		{"g-recaptcha-response", captchaToken},
		{"submit_app", "Sending..."},
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return "", nil, err
		}
	}

	name := req.CVFilename
	if strings.TrimSpace(name) == "" {
		name = "cv.pdf"
	}
	part, err := w.CreateFormFile("cv", name)
	if err != nil {
		return "", nil, err
	}
	if _, err := part.Write(req.CVBytes); err != nil {
		return "", nil, err
	}

	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return w.FormDataContentType(), buf, nil
}

// coverLetterOrFallback returns the tailored cover letter, or a short
// generic body when the pipeline produced none (dev/smoke paths only).
func coverLetterOrFallback(req autoapply.SubmitRequest) string {
	if cl := strings.TrimSpace(req.CoverLetter); cl != "" {
		return cl
	}
	return fmt.Sprintf("Dear Hiring Manager,\n\nPlease find my application and CV attached.\n\nBest regards,\n%s", strings.TrimSpace(req.FullName))
}

// locationOptions maps lower-cased option labels to their numeric ids from
// the form's <select name="sender_location">.
func locationOptions(body []byte) map[string]string {
	out := map[string]string{}
	sel := selectBlock(body, "sender_location")
	if sel == nil {
		return out
	}
	for _, m := range optionRE.FindAllSubmatch(sel, -1) {
		label := strings.ToLower(strings.TrimSpace(string(m[2])))
		if label != "" {
			out[label] = string(m[1])
		}
	}
	return out
}

// matchLocationID picks the option id whose label best matches the
// candidate's preferred location. PreferredLocations may be a
// comma-separated list; the first token with a match wins.
func matchLocationID(opts map[string]string, candidateLoc string) string {
	if len(opts) == 0 {
		return ""
	}
	for tok := range strings.SplitSeq(strings.ToLower(candidateLoc), ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if id, ok := opts[tok]; ok {
			return id
		}
		for label, id := range opts {
			if strings.Contains(tok, label) || strings.Contains(label, tok) {
				return id
			}
		}
	}
	return ""
}

// selectBlock returns the bytes of the <select name="want"> … </select>
// element, or nil when absent.
func selectBlock(body []byte, want string) []byte {
	marker := []byte(`name="` + want + `"`)
	i := bytes.Index(body, marker)
	if i < 0 {
		return nil
	}
	rest := body[i:]
	before, _, found := bytes.Cut(rest, []byte("</select>"))
	if !found {
		return rest
	}
	return before
}

// resolveAction returns the apply form's POST target, resolving the form's
// action against the page URL. MyJobMag posts to itself (action=""), so the
// page URL is the usual result.
func resolveAction(body []byte, formURL string) string {
	action := firstSubmatch(applyFormActionRE, body)
	if strings.TrimSpace(action) == "" {
		return formURL
	}
	base, err := url.Parse(formURL)
	if err != nil {
		return formURL
	}
	ref, err := url.Parse(action)
	if err != nil {
		return formURL
	}
	return base.ResolveReference(ref).String()
}

func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// alreadyApplied reports whether the response is MyJobMag's duplicate
// notice ("It seems you have applied for this job before."). This is a 200
// re-render, not a fresh submission, so it must not be recorded as applied.
func alreadyApplied(body []byte) bool {
	lower := bytes.ToLower(body)
	for _, m := range [][]byte{
		[]byte("have applied for this job before"),
		[]byte("you have applied for this"),
		[]byte("already applied"),
	} {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

// looksLikeFormError detects explicit failure signals in a 2xx response so
// a silent rejection isn't recorded as a success. Markers are kept
// specific — the apply form itself contains "recaptcha", so only
// unambiguous error phrases are matched (best-effort; the exact success
// copy isn't verified against a live solved submission).
func looksLikeFormError(body []byte) bool {
	lower := bytes.ToLower(body)
	for _, m := range [][]byte{
		[]byte("invalid captcha"),
		[]byte("captcha verification failed"),
		[]byte("please complete the captcha"),
		[]byte("the cv field is required"),
	} {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

// previewBody returns the first ~600 chars of a response as a printable
// single line, for diagnosing the POST outcome (e.g. the real success or
// error copy MyJobMag renders).
func previewBody(b []byte) string {
	const max = 600
	if len(b) > max {
		b = b[:max]
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func firstSubmatch(re *regexp.Regexp, body []byte) string {
	m := re.FindSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return string(m[1])
}
