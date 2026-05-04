package autoapply

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestEmailFallback_CanHandleMailto(t *testing.T) {
	s := NewEmailFallback("", 0, "", "")
	assert.True(t, s.CanHandle("", "mailto:hr@co.com"))
	assert.True(t, s.CanHandle("", "MAILTO:hr@co.com"))
	assert.False(t, s.CanHandle("", "https://co.com/apply"))
}

func TestEmailFallback_NoSMTPSkips(t *testing.T) {
	s := NewEmailFallback("", 587, "", "")
	res, err := s.Submit(nil, SubmitRequest{ApplyURL: "mailto:hr@co.com"})
	require.NoError(t, err)
	assert.Equal(t, "skipped", res.Method)
	assert.Equal(t, "no_smtp", res.SkipReason)
}

func TestParseMailtoRecipient(t *testing.T) {
	cases := map[string]string{
		"mailto:hr@co.com":                   "hr@co.com",
		"mailto:hr@co.com?subject=Apply":     "hr@co.com",
		"mailto:Hiring%20Team%3Chr@co.com%3E": "hr@co.com",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := parseMailtoRecipient(in)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestParseMailtoRecipient_BadInput(t *testing.T) {
	for _, bad := range []string{
		"https://x/y",
		"mailto:not-an-address",
		"mailto:",
	} {
		t.Run(bad, func(t *testing.T) {
			got, err := parseMailtoRecipient(bad)
			if err == nil {
				assert.Empty(t, got)
			}
		})
	}
}

func TestBuildEmailBody_HasRequiredHeaders(t *testing.T) {
	body, err := buildEmailBody("autoapply@stawi.io", "hr@co.com", SubmitRequest{
		FullName:   "Ada Lovelace",
		Email:      "ada@example.com",
		Phone:      "+1-555-0100",
		CVBytes:    []byte("PDF"),
		CVFilename: "ada.pdf",
	})
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "From: autoapply@stawi.io")
	assert.Contains(t, s, "To: hr@co.com")
	assert.Contains(t, s, "Reply-To: ada@example.com")
	assert.Contains(t, s, "Subject:")
	assert.Contains(t, s, "Date: ")
	assert.Contains(t, s, "Message-ID: <")
	assert.Contains(t, s, "MIME-Version: 1.0")
	assert.Contains(t, s, "Content-Type: multipart/mixed; boundary=")
	assert.Contains(t, s, "Content-Disposition: attachment; filename=\"ada.pdf\"")
	// base64 line-wrapped at 76
	for _, line := range strings.Split(s, "\r\n") {
		require.LessOrEqual(t, len(line), 998, "RFC 5322 max line length")
	}
}

func TestSafeAttachmentName(t *testing.T) {
	assert.Equal(t, "resume.pdf", safeAttachmentName(""))
	assert.Equal(t, "cv.pdf", safeAttachmentName("/abs/cv.pdf"))
	assert.Equal(t, "passwd", safeAttachmentName("../etc/passwd"))
}

// Sanity: domain.SourceType is unused in this test file but re-exporting
// it keeps the dependency graph clean for future tests of CanHandle.
var _ = domain.SourceGreenhouse
