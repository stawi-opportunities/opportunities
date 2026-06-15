package service

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/otprendezvous"
)

// maxEmailBytes caps the raw message we'll parse from the inbound provider.
const maxEmailBytes = 1 << 20 // 1 MiB

// OTPIngress is the slice of the rendezvous the webhook needs: deliver a
// code and dedup provider redeliveries.
type OTPIngress interface {
	Put(ctx context.Context, key, code string) error
	MarkSeen(ctx context.Context, messageID string) (bool, error)
}

// NewOTPWebhookHandler returns the inbound-email webhook that turns a
// forwarded Greenhouse security-code email into an rdv.Put. It is the
// production replacement for the Phase-2 manual injector: identical Put +
// key, with the email as the data source.
//
// The inbound-parse provider authenticates with the shared secret; we
// additionally require the original sender to sit under
// allowedSenderDomain (e.g. greenhouse-mail.io; subdomains accepted) as a
// lightweight anti-spoof. Full DKIM verification is a future hardening
// step.
//
// Correlation: the candidate is the original recipient (forwarding
// preserves To/Delivered-To), the company comes from the subject, and the
// code is anchored-extracted from the body. Together they form the same
// rendezvous key the Greenhouse submitter is polling.
func NewOTPWebhookHandler(rdv OTPIngress, secret, allowedSenderDomain string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validOTPSecret(r, secret) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		log := util.Log(r.Context())

		raw, err := io.ReadAll(io.LimitReader(r.Body, maxEmailBytes))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
		if err != nil {
			http.Error(w, "parse email", http.StatusBadRequest)
			return
		}

		from := headerAddress(msg.Header.Get("From"))
		if !senderDomainAllowed(from, allowedSenderDomain) {
			// Junk/spoof — ack so the provider doesn't retry it forever.
			log.WithField("from", from).Warn("autoapply: OTP webhook rejected sender domain")
			w.WriteHeader(http.StatusAccepted)
			return
		}

		recipient := firstNonEmpty(
			headerAddress(msg.Header.Get("To")),
			headerAddress(msg.Header.Get("Delivered-To")),
			headerAddress(msg.Header.Get("X-Original-To")),
		)
		company := otprendezvous.CompanyFromSubject(msg.Header.Get("Subject"))
		body, _ := readTextBody(msg)
		code := otprendezvous.ExtractCode(body)

		if recipient == "" || company == "" || code == "" {
			log.WithField("have_recipient", recipient != "").
				WithField("have_company", company != "").
				WithField("have_code", code != "").
				Warn("autoapply: OTP webhook could not correlate email")
			w.WriteHeader(http.StatusAccepted) // nothing actionable; don't retry
			return
		}

		// Drop provider redeliveries so a duplicate can't replace a fresh
		// code for a later attempt to the same (candidate, company).
		if id := strings.Trim(msg.Header.Get("Message-Id"), "<> "); id != "" {
			first, derr := rdv.MarkSeen(r.Context(), id)
			if derr == nil && !first {
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}

		key := otprendezvous.Key(recipient, company)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := rdv.Put(ctx, key, code); err != nil {
			http.Error(w, "put", http.StatusBadGateway)
			return
		}

		log.WithField("recipient", recipient).
			WithField("company", company).
			Info("autoapply: OTP code delivered via webhook")
		w.WriteHeader(http.StatusAccepted)
	})
}

// senderDomainAllowed reports whether from's domain equals allowed or is a
// subdomain of it. An empty allowed domain disables the check.
func senderDomainAllowed(from, allowed string) bool {
	if allowed == "" {
		return true
	}
	at := strings.LastIndex(from, "@")
	if at < 0 {
		return false
	}
	dom := strings.ToLower(from[at+1:])
	allowed = strings.ToLower(allowed)
	return dom == allowed || strings.HasSuffix(dom, "."+allowed)
}

// headerAddress parses a "Name <addr@host>" header into a lowercased bare
// address, falling back to the trimmed raw value.
func headerAddress(h string) string {
	if h == "" {
		return ""
	}
	if a, err := mail.ParseAddress(h); err == nil {
		return strings.ToLower(a.Address)
	}
	return strings.ToLower(strings.TrimSpace(h))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// readTextBody returns the text content of the message, preferring
// text/plain and falling back to text/html for simple multipart bodies.
func readTextBody(msg *mail.Message) (string, error) {
	ctype := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		b, rerr := io.ReadAll(io.LimitReader(msg.Body, maxEmailBytes))
		return string(b), rerr
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	var htmlFallback string
	for {
		part, perr := mr.NextPart()
		if perr != nil {
			break // io.EOF or malformed — stop with what we have
		}
		pType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		b, _ := io.ReadAll(io.LimitReader(part, maxEmailBytes))
		switch {
		case strings.HasPrefix(pType, "text/plain"):
			return string(b), nil
		case strings.HasPrefix(pType, "text/html"):
			htmlFallback = string(b)
		}
	}
	return htmlFallback, nil
}
