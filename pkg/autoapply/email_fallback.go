package autoapply

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// EmailFallback is the Tier-3 last-resort submitter. It sends the
// candidate's CV as an email attachment to the HR address extracted
// from a mailto: apply URL. When SMTP is unconfigured it returns a
// skipped/no_smtp result without error.
type EmailFallback struct {
	host     string
	port     int
	from     string
	password string
}

// NewEmailFallback wires the fallback. host/from/password may be empty
// — the submitter degrades gracefully to a skip result.
func NewEmailFallback(host string, port int, from, password string) *EmailFallback {
	return &EmailFallback{host: host, port: port, from: from, password: password}
}

func (s *EmailFallback) Name() string { return "email" }

func (s *EmailFallback) CanHandle(_ domain.SourceType, applyURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(applyURL)), "mailto:")
}

func (s *EmailFallback) Submit(_ context.Context, req SubmitRequest) (SubmitResult, error) {
	if s.host == "" || s.from == "" {
		return SubmitResult{Method: "skipped", SkipReason: "no_smtp"}, nil
	}

	to, err := parseMailtoRecipient(req.ApplyURL)
	if err != nil || to == "" {
		return SubmitResult{Method: "skipped", SkipReason: "empty_recipient"}, nil
	}

	body, err := buildEmailBody(s.from, to, req)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("email: build body: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	auth := smtp.PlainAuth("", s.from, s.password, s.host)
	if err := smtp.SendMail(addr, auth, s.from, []string{to}, body); err != nil {
		return SubmitResult{}, fmt.Errorf("email: send: %w", err)
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

// buildEmailBody constructs an RFC 5322 / 2045 multipart message.
// `from` is the SMTP envelope From (must match the From: header to
// pass DMARC); the candidate's address goes in Reply-To so replies
// land back with them.
func buildEmailBody(from, to string, req SubmitRequest) ([]byte, error) {
	var bodyBuf bytes.Buffer
	w := multipart.NewWriter(&bodyBuf)

	textH := make(textproto.MIMEHeader)
	textH.Set("Content-Type", "text/plain; charset=utf-8")
	textH.Set("Content-Transfer-Encoding", "7bit")
	textPart, err := w.CreatePart(textH)
	if err != nil {
		return nil, err
	}
	plain := fmt.Sprintf(`Dear Hiring Manager,

Please find attached my CV for the position.

Name: %s
Email: %s
Phone: %s
Title: %s

%s

Best regards,
%s
`, req.FullName, req.Email, req.Phone, req.CurrentTitle, req.CoverLetter, req.FullName)
	if _, err := textPart.Write([]byte(plain)); err != nil {
		return nil, err
	}

	if len(req.CVBytes) > 0 {
		attachH := make(textproto.MIMEHeader)
		attachH.Set("Content-Type", "application/octet-stream")
		attachH.Set("Content-Transfer-Encoding", "base64")
		attachH.Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", safeAttachmentName(req.CVFilename)))
		attachPart, err := w.CreatePart(attachH)
		if err != nil {
			return nil, err
		}
		if err := writeBase64Wrapped(attachPart, req.CVBytes); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	subject := fmt.Sprintf("Application from %s", req.FullName)

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	if req.Email != "" && req.Email != from {
		fmt.Fprintf(&msg, "Reply-To: %s\r\n", req.Email)
	}
	fmt.Fprintf(&msg, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Message-ID: <%s@autoapply>\r\n", xid.New().String())
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%q\r\n", w.Boundary())
	msg.WriteString("\r\n")
	msg.Write(bodyBuf.Bytes())
	return msg.Bytes(), nil
}

// writeBase64Wrapped writes data as base64 with CRLF every 76 chars,
// per RFC 2045.
func writeBase64Wrapped(w interface{ Write([]byte) (int, error) }, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 0 {
		n := 76
		if n > len(enc) {
			n = len(enc)
		}
		if _, err := w.Write([]byte(enc[:n] + "\r\n")); err != nil {
			return err
		}
		enc = enc[n:]
	}
	return nil
}

// safeAttachmentName drops path components and anything other than
// [A-Za-z0-9._-], falling back to "resume.pdf" when the input is empty.
func safeAttachmentName(name string) string {
	name = path.Base(name)
	if name == "." || name == "/" || name == "" {
		return "resume.pdf"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "resume.pdf"
	}
	return b.String()
}
