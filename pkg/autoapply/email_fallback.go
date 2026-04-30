package autoapply

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
	"strings"

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

	to := strings.TrimPrefix(req.ApplyURL, "mailto:")
	to = strings.TrimPrefix(to, "Mailto:")
	if idx := strings.IndexAny(to, "?#"); idx >= 0 {
		to = to[:idx]
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return SubmitResult{Method: "skipped", SkipReason: "empty_recipient"}, nil
	}

	body, err := buildEmailBody(req)
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

func buildEmailBody(req SubmitRequest) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	subject := fmt.Sprintf("Application from %s", req.FullName)
	header := fmt.Sprintf(
		"From: %s\r\nTo: \r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n",
		req.Email, subject, w.Boundary(),
	)
	buf.Reset()
	buf.WriteString(header)

	// Re-create writer against the buffer that has headers.
	var bodyBuf bytes.Buffer
	w2 := multipart.NewWriter(&bodyBuf)

	textH := make(textproto.MIMEHeader)
	textH.Set("Content-Type", "text/plain; charset=utf-8")
	textPart, err := w2.CreatePart(textH)
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("Dear Hiring Manager,\n\nPlease find attached my CV for the position.\n\nName: %s\nEmail: %s\nPhone: %s\nTitle: %s\n\n%s\n\nBest regards,\n%s",
		req.FullName, req.Email, req.Phone, req.CurrentTitle, req.CoverLetter, req.FullName)
	if _, err := textPart.Write([]byte(body)); err != nil {
		return nil, err
	}

	if len(req.CVBytes) > 0 {
		attachH := make(textproto.MIMEHeader)
		attachH.Set("Content-Type", "application/octet-stream")
		attachH.Set("Content-Transfer-Encoding", "base64")
		attachH.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", req.CVFilename))
		attachPart, err := w2.CreatePart(attachH)
		if err != nil {
			return nil, err
		}
		enc := base64.StdEncoding.EncodeToString(req.CVBytes)
		if _, err := attachPart.Write([]byte(enc)); err != nil {
			return nil, err
		}
	}
	_ = w2.Close()
	_ = w.Close()

	// Build final: headers + multipart body.
	finalBuf := bytes.Buffer{}
	finalBuf.WriteString(fmt.Sprintf(
		"From: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n",
		req.Email, subject, w2.Boundary(),
	))
	finalBuf.Write(bodyBuf.Bytes())
	return finalBuf.Bytes(), nil
}
