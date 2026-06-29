package myjobmagsubmitter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// stubSender records the last Send call.
type stubSender struct {
	configured bool
	called     bool
	to         string
	subject    string
	req        autoapply.SubmitRequest
}

func (s *stubSender) Configured() bool { return s.configured }
func (s *stubSender) Send(_ context.Context, to, subject string, req autoapply.SubmitRequest) error {
	s.called, s.to, s.subject, s.req = true, to, subject, req
	return nil
}

func TestCanHandle(t *testing.T) {
	s := New(Config{Sender: &stubSender{configured: true}})
	// Every edition is claimed.
	for _, u := range []string{
		"https://www.myjobmag.com/job/x",          // Nigeria / flagship
		"https://www.myjobmag.co.ke/job/x",        // Kenya
		"https://www.myjobmag.co.za/job/x",        // South Africa
		"https://www.myjobmagghana.com/job/x",     // Ghana (different domain shape)
		"https://www.myjobmag.co.uk/job/x",        // UK
	} {
		assert.True(t, s.CanHandle(domain.SourceMyJobMag, u), u)
	}
	// wrong source
	assert.False(t, s.CanHandle(domain.SourceJobberman, "https://www.myjobmag.co.ke/job/x"))
	// wrong host
	assert.False(t, s.CanHandle(domain.SourceMyJobMag, "https://www.brightermonday.co.ke/job/x"))
}

const listingWithEmail = `<html><body>
<h1>Company Secretary Associate</h1>
<div class="job-details">Some description of the role.</div>
<h2>Method of Application</h2>
<p>Interested and qualified candidates should forward their CV to:
<a href="mailto:careers@fanisi.net">careers@fanisi.net</a> using the position as subject of email.</p>
</body></html>`

const listingNoEmail = `<html><body>
<h1>Frontend Engineer</h1>
<h2>Method of Application</h2>
<p>Interested and qualified? <a href="https://acme.com/apply">Click here to apply</a></p>
</body></html>`

func TestParseMethodOfApplication(t *testing.T) {
	email, title := parseMethodOfApplication([]byte(listingWithEmail))
	assert.Equal(t, "careers@fanisi.net", email)
	assert.Equal(t, "Company Secretary Associate", title)

	email, title = parseMethodOfApplication([]byte(listingNoEmail))
	assert.Empty(t, email)
	assert.Equal(t, "Frontend Engineer", title)
}

func TestParseMethodOfApplication_IgnoresSiteOwnEmail(t *testing.T) {
	const body = `<html><body><h1>Role</h1>
<footer>Contact support@myjobmag.co.ke</footer>
<h2>Method of Application</h2><p>email us at support@myjobmag.co.ke</p></body></html>`
	email, _ := parseMethodOfApplication([]byte(body))
	assert.Empty(t, email, "site's own address must not be treated as the recipient")
}

func TestSubmit_SendsWhenEmailPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(listingWithEmail))
	}))
	defer srv.Close()

	sender := &stubSender{configured: true}
	s := New(Config{Sender: sender})
	// CanHandle is host-gated to myjobmag.*, but Submit itself doesn't
	// re-check the host, so the test server URL is fine here.
	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceMyJobMag,
		ApplyURL:   srv.URL,
		FullName:   "Ada Lovelace",
		Email:      "ada@example.com",
		CVRef:      "https://files/cv/ada.pdf",
	})
	require.NoError(t, err)
	assert.Equal(t, "myjobmag_email", res.Method)
	assert.True(t, sender.called)
	assert.Equal(t, "careers@fanisi.net", sender.to)
	assert.Equal(t, "Company Secretary Associate", sender.subject)
}

func TestSubmit_SkipsWhenNoEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(listingNoEmail))
	}))
	defer srv.Close()

	sender := &stubSender{configured: true}
	s := New(Config{Sender: sender})
	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceMyJobMag,
		ApplyURL:   srv.URL,
	})
	require.NoError(t, err)
	assert.Equal(t, "skipped", res.Method)
	assert.Equal(t, "no_email", res.SkipReason)
	assert.False(t, sender.called)
}

func TestSubmit_SkipsWhenSenderUnconfigured(t *testing.T) {
	s := New(Config{Sender: &stubSender{configured: false}})
	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType: domain.SourceMyJobMag,
		ApplyURL:   "https://www.myjobmag.co.ke/job/x",
	})
	require.NoError(t, err)
	assert.Equal(t, "skipped", res.Method)
	assert.Equal(t, "no_sender", res.SkipReason)
}
