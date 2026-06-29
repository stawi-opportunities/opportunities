package autoapply

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// stubSender is a test EmailSender that records the last Send call.
type stubSender struct {
	configured bool
	to         string
	subject    string
	req        SubmitRequest
	called     bool
	err        error
}

func (s *stubSender) Configured() bool { return s.configured }

func (s *stubSender) Send(_ context.Context, to, subject string, req SubmitRequest) error {
	s.called = true
	s.to, s.subject, s.req = to, subject, req
	return s.err
}

func TestEmailFallback_CanHandleMailto(t *testing.T) {
	s := NewEmailFallback(&stubSender{})
	assert.True(t, s.CanHandle("", "mailto:hr@co.com"))
	assert.True(t, s.CanHandle("", "MAILTO:hr@co.com"))
	assert.False(t, s.CanHandle("", "https://co.com/apply"))
}

func TestEmailFallback_NoSenderSkips(t *testing.T) {
	for _, s := range []*EmailFallback{
		NewEmailFallback(nil),
		NewEmailFallback(&stubSender{configured: false}),
	} {
		res, err := s.Submit(context.TODO(), SubmitRequest{ApplyURL: "mailto:hr@co.com"})
		require.NoError(t, err)
		assert.Equal(t, "skipped", res.Method)
		assert.Equal(t, "no_sender", res.SkipReason)
	}
}

func TestEmailFallback_SendsToMailtoRecipient(t *testing.T) {
	sender := &stubSender{configured: true}
	s := NewEmailFallback(sender)
	res, err := s.Submit(context.TODO(), SubmitRequest{
		ApplyURL: "mailto:hr@co.com?subject=Apply",
		FullName: "Ada Lovelace",
	})
	require.NoError(t, err)
	assert.Equal(t, "email", res.Method)
	assert.True(t, sender.called)
	assert.Equal(t, "hr@co.com", sender.to)
}

func TestParseMailtoRecipient(t *testing.T) {
	cases := map[string]string{
		"mailto:hr@co.com":                    "hr@co.com",
		"mailto:hr@co.com?subject=Apply":      "hr@co.com",
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

// Sanity: domain.SourceType is unused in this test file but re-exporting
// it keeps the dependency graph clean for future tests of CanHandle.
var _ = domain.SourceGreenhouse
