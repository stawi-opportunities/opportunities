// Package notifyemail implements autoapply.EmailSender on top of the
// antinvestor Notification service.
//
// Job applications are delivered as outbound email notifications. The
// SOURCE is the candidate's own email (so the application is from our
// client) and the RECIPIENT is the employer address; the tailored cover
// letter is the pre-rendered body (Notification.Data), and the candidate's
// CV travels by REFERENCE — its URL/id under Extras["attachment"]. The
// notification backend resolves that reference to the actual file and
// attaches it just before the email is sent, so no CV bytes pass through
// the autoapply service. The subject rides in Extras since the
// Notification message has no dedicated field for it.
//
// Authenticating to the Notification service uses our own service
// credentials — no candidate mailbox password is involved; the candidate's
// address is only identity metadata on the source contact.
package notifyemail

import (
	"context"
	"fmt"
	"strings"

	commonv1 "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
)

// Extras keys understood by the notification backend for an application
// email. attachment carries the CV reference the backend resolves and
// attaches at send time.
const (
	extrasSubject    = "subject"
	extrasAttachment = "attachment"
)

// Sender delivers application emails through the Notification service.
type Sender struct {
	client notificationv1connect.NotificationServiceClient
}

// New constructs a Sender. A nil client (Notification service unconfigured)
// makes Configured() return false so callers degrade to a skip.
func New(client notificationv1connect.NotificationServiceClient) *Sender {
	return &Sender{client: client}
}

// Configured reports whether the Notification client is wired.
func (s *Sender) Configured() bool { return s != nil && s.client != nil }

// Send queues an application email to `to`, sent as the candidate
// (req.Email). A blank subject falls back to "Application from <FullName>".
func (s *Sender) Send(ctx context.Context, to, subject string, req autoapply.SubmitRequest) error {
	if !s.Configured() {
		return fmt.Errorf("notifyemail: send called on unconfigured sender")
	}
	if strings.TrimSpace(subject) == "" {
		subject = fmt.Sprintf("Application from %s", strings.TrimSpace(req.FullName))
	}

	extras := map[string]any{extrasSubject: subject}
	if req.CVRef != "" {
		extras[extrasAttachment] = req.CVRef
	}
	extrasStruct, err := structpb.NewStruct(extras)
	if err != nil {
		return fmt.Errorf("notifyemail: build extras: %w", err)
	}

	note := notificationv1.Notification_builder{
		Type:        "email",
		OutBound:    true,
		AutoRelease: true,
		// Source is the candidate so the application is "from" our client;
		// recipient is the employer address scraped from the listing.
		Source:    commonv1.ContactLink_builder{ProfileName: req.FullName, Detail: req.Email}.Build(),
		Recipient: commonv1.ContactLink_builder{Detail: to}.Build(),
		Data:      emailBody(req),
		Extras:    extrasStruct,
	}.Build()

	reqMsg := notificationv1.SendRequest_builder{
		Data: []*notificationv1.Notification{note},
	}.Build()

	stream, err := s.client.Send(ctx, connect.NewRequest(reqMsg))
	if err != nil {
		return fmt.Errorf("notifyemail: send: %w", err)
	}
	// Send is a server-streaming RPC; drain it so the request completes
	// and surfaces any delivery-queueing error, then release the stream.
	for stream.Receive() {
	}
	if cerr := stream.Err(); cerr != nil {
		_ = stream.Close()
		return fmt.Errorf("notifyemail: stream: %w", cerr)
	}
	return stream.Close()
}

// emailBody renders the plain-text message: the tailored cover letter the
// matching pipeline generated, framed with the candidate's contact line.
func emailBody(req autoapply.SubmitRequest) string {
	var b strings.Builder
	b.WriteString("Dear Hiring Manager,\n\n")
	if cl := strings.TrimSpace(req.CoverLetter); cl != "" {
		b.WriteString(cl)
	} else {
		b.WriteString("Please find my application for the role. My CV is attached.")
	}
	b.WriteString("\n\nBest regards,\n")
	b.WriteString(strings.TrimSpace(req.FullName))
	if contact := contactLine(req); contact != "" {
		b.WriteString("\n")
		b.WriteString(contact)
	}
	return b.String()
}

func contactLine(req autoapply.SubmitRequest) string {
	var parts []string
	if req.Email != "" {
		parts = append(parts, req.Email)
	}
	if req.Phone != "" {
		parts = append(parts, req.Phone)
	}
	return strings.Join(parts, " | ")
}
