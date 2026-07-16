package myjobmagsubmitter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

type stubCaptcha struct {
	token  string
	err    error
	gotKey string
	gotURL string
	calls  int
}

func (c *stubCaptcha) SolveRecaptchaV2(_ context.Context, siteKey, pageURL string) (string, error) {
	c.calls++
	c.gotKey, c.gotURL = siteKey, pageURL
	return c.token, c.err
}

const applyFormHTML = `<html><body>
<h1>Full Stack Developer</h1>
<form id="d-apply-form" action="" enctype="multipart/form-data" method="post">
  <input name="sender_name" required value="" />
  <input name="sender_email" required value="" />
  <input name="sender_phone" required value="" />
  <select name="sender_location" required>
    <option value="">- Select location -</option>
    <option  value="124">Nairobi</option>
    <option  value="123">Mombasa</option>
  </select>
  <textarea name="apply_body" required></textarea>
  <input name="cv" type="file" />
  <div class="g-recaptcha" data-sitekey="6LfdhykTAAAAABPgxCUlOle6B-hyZr6nS9Re90Jb"></div>
</form>
<script src="https://www.google.com/recaptcha/api.js"></script>
</body></html>`

func TestFindApplyFormURL(t *testing.T) {
	// Page is itself the form.
	assert.Equal(t, "https://www.myjobmag.co.ke/job-application/123",
		findApplyFormURL([]byte("<html></html>"), "https://www.myjobmag.co.ke/job-application/123"))
	// Listing links to the form.
	listing := `<a href="/job-application/456">Apply Now</a>`
	assert.Equal(t, "https://www.myjobmag.co.ke/job-application/456",
		findApplyFormURL([]byte(listing), "https://www.myjobmag.co.ke/job/x"))
	// No on-site form.
	assert.Empty(t, findApplyFormURL([]byte(`<a href="https://acme.com/apply">x</a>`), "https://www.myjobmag.co.ke/job/x"))
}

func TestLocationOptionsAndMatch(t *testing.T) {
	opts := locationOptions([]byte(applyFormHTML))
	assert.Equal(t, "124", opts["nairobi"])
	assert.Equal(t, "123", opts["mombasa"])

	assert.Equal(t, "124", matchLocationID(opts, "Nairobi, Kenya"))
	assert.Equal(t, "123", matchLocationID(opts, "Mombasa"))
	assert.Empty(t, matchLocationID(opts, "Kampala"))
}

func TestSubmitViaForm_PostsApplication(t *testing.T) {
	var gotFields map[string]string
	var gotCVName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(applyFormHTML))
			return
		}
		require.NoError(t, r.ParseMultipartForm(1<<20))
		gotFields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			gotFields[k] = v[0]
		}
		if fhs := r.MultipartForm.File["cv"]; len(fhs) > 0 {
			gotCVName = fhs[0].Filename
		}
		_, _ = w.Write([]byte("<html>Your application has been sent</html>"))
	}))
	defer srv.Close()

	cap := &stubCaptcha{token: "TOKEN123"}
	s := New(Config{Captcha: cap})

	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType:  domain.SourceMyJobMag,
		ApplyURL:    srv.URL + "/job-application/789",
		FullName:    "Joakim Bwire",
		Email:       "joakim@example.com",
		Phone:       "0741697305",
		Location:    "Nairobi",
		CoverLetter: "Dear Hiring Manager, I am keen on this role.",
		CVBytes:     []byte("%PDF-1.4 fake"),
		CVFilename:  "cv.pdf",
	})
	require.NoError(t, err)
	assert.Equal(t, "myjobmag_form", res.Method)
	assert.Equal(t, "789", res.ExternalRef)

	// Captcha solved against the right site key + page URL.
	assert.Equal(t, "6LfdhykTAAAAABPgxCUlOle6B-hyZr6nS9Re90Jb", cap.gotKey)
	assert.Equal(t, srv.URL+"/job-application/789", cap.gotURL)

	// POST carried the expected fields.
	assert.Equal(t, "789", gotFields["position"])
	assert.Equal(t, "Joakim Bwire", gotFields["sender_name"])
	assert.Equal(t, "joakim@example.com", gotFields["sender_email"])
	assert.Equal(t, "124", gotFields["sender_location"])
	assert.Equal(t, "TOKEN123", gotFields["g-recaptcha-response"])
	assert.Equal(t, "Sending...", gotFields["submit_app"])
	assert.True(t, strings.Contains(gotFields["apply_body"], "keen on this role"))
	assert.Equal(t, "cv.pdf", gotCVName)
}

func TestSubmitViaForm_SkipsWithoutCaptchaSolver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(applyFormHTML))
	}))
	defer srv.Close()

	s := New(Config{Captcha: nil})
	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceMyJobMag,
		ApplyURL:   srv.URL + "/job-application/1",
		CVBytes:    []byte("pdf"),
		Location:   "Nairobi",
	})
	require.NoError(t, err)
	assert.Equal(t, "skipped", res.Method)
	assert.Equal(t, "captcha_unavailable", res.SkipReason)
}

func TestSubmitViaForm_SkipsWhenLocationUnmatched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(applyFormHTML))
	}))
	defer srv.Close()

	s := New(Config{Captcha: &stubCaptcha{token: "t"}})
	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceMyJobMag,
		ApplyURL:   srv.URL + "/job-application/1",
		CVBytes:    []byte("pdf"),
		Location:   "Kampala", // not in the option list
	})
	require.NoError(t, err)
	assert.Equal(t, "skipped", res.Method)
	assert.Equal(t, "location_unmatched", res.SkipReason)
}
