package autoapply

import (
	"context"
	"fmt"
	"net/mail"
	"net/url"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// EmailSender delivers an application email to an employer address via the
// antinvestor Notification service. The recipient is resolved by the caller
// (parsed from a mailto: apply URL, or scraped from a listing's "Method of
// Application" block); the sender is responsible only for delivery.
//
// The CV is NOT inlined here — it travels as an opaque reference
// (req.CVRef) that the notification backend resolves to the actual file and
// attaches just before the message is sent. A blank subject falls back to a
// generic "Application from <FullName>".
type EmailSender interface {
	// Configured reports whether the sender is wired enough to attempt a
	// send. Callers map a false here to a skipped/no_sender result.
	Configured() bool
	// Send queues the application carried by req for delivery to `to` with
	// `subject`.
	Send(ctx context.Context, to, subject string, req SubmitRequest) error
}

// EmailFallback is the Tier-3 last-resort submitter. It queues the
// candidate's application to the HR address extracted from a mailto: apply
// URL via the configured EmailSender. When the sender is unconfigured it
// returns a skipped/no_sender result without error.
type EmailFallback struct {
	sender EmailSender
}

// NewEmailFallback wires the fallback over the given EmailSender. A nil
// sender degrades gracefully to a no_sender skip.
func NewEmailFallback(sender EmailSender) *EmailFallback {
	return &EmailFallback{sender: sender}
}

func (s *EmailFallback) Name() string { return "email" }

func (s *EmailFallback) CanHandle(_ domain.SourceType, applyURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(applyURL)), "mailto:")
}

func (s *EmailFallback) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
	if s.sender == nil || !s.sender.Configured() {
		return SubmitResult{Method: "skipped", SkipReason: "no_sender"}, nil
	}

	to, err := parseMailtoRecipient(req.ApplyURL)
	if err != nil || to == "" {
		return SubmitResult{Method: "skipped", SkipReason: "empty_recipient"}, nil
	}

	if err := s.sender.Send(ctx, to, "", req); err != nil {
		return SubmitResult{}, err
	}

	return SubmitResult{Method: "email"}, nil
}

// parseMailtoRecipient extracts the first recipient from a mailto: URL,
// validating the address with net/mail.
func parseMailtoRecipient(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Scheme, "mailto") {
		return "", fmt.Errorf("not a mailto URL: %s", u.Scheme)
	}
	// u.Opaque is percent-encoded; decode before parsing as an
	// address list so "Hiring%20Team%3Chr@co.com%3E" becomes
	// "Hiring Team<hr@co.com>".
	decoded, err := url.QueryUnescape(u.Opaque)
	if err != nil {
		decoded = u.Opaque
	}
	addrs, err := mail.ParseAddressList(decoded)
	if err != nil || len(addrs) == 0 {
		return "", fmt.Errorf("invalid mailto recipient: %w", err)
	}
	return addrs[0].Address, nil
}
