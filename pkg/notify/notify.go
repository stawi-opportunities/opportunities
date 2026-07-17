// Package notify queues outbound messages through service-notification using
// the same constructs as service-profile (and other antinvestor services):
//
//	notificationv1connect.NotificationServiceClient
//	notificationv1.Notification{ Template, Payload, Recipient ContactLink, OutBound, AutoRelease }
//	connection.NewServiceClient(..., servicecatalog.ServiceNotification, ...)
//
// Callers supply a template name (usually from config MessageTemplate*) and a
// variables map; this package does not invent product-specific notifier APIs.
package notify

import (
	"context"
	"database/sql"
	"fmt"

	commonv1 "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
	"github.com/pitabwire/util"
	"google.golang.org/protobuf/types/known/structpb"
)

// Default template names (override via MESSAGE_TEMPLATE_* env, same pattern as
// profile's MESSAGE_TEMPLATE_CONTACT_VERIFICATION).
const (
	DefaultTemplateMatchesReady     = "template.opportunities.matches.ready"
	DefaultTemplateMatchesDigest    = "template.opportunities.matches.digest"
	DefaultTemplateWeeklyJobsDigest = "template.opportunities.weekly_jobs.digest"
	DefaultTemplateCVStaleNudge     = "template.opportunities.cv.stale_nudge"
)

// Message is one templated outbound notification — same fields profile uses
// when building notificationv1.Notification for ContactVerificationQueue.
type Message struct {
	// Template is the service-notification template name/id.
	Template string
	// ProfileID is the recipient profile (ContactLink.ProfileId). Required.
	ProfileID string
	// ContactID optional ContactLink.ContactId when known.
	ContactID string
	// Language optional preferred language code.
	Language string
	// Variables become Notification.Payload (template placeholders).
	Variables map[string]any
	// Priority defaults to PRIORITY_LOW when unset (zero value is HIGH in the
	// enum, so callers should set explicitly when needed).
	Priority notificationv1.PRIORITY
	// PrioritySet when true uses Priority; when false defaults to LOW.
	PrioritySet bool
}

// Send queues msg via NotificationService.Send. Mirrors profile's
// ContactVerificationQueue.Execute send path: build Notification, Send,
// drain stream. Returns error on client/RPC failure (callers may log and continue).
func Send(ctx context.Context, cli notificationv1connect.NotificationServiceClient, msg Message) error {
	log := util.Log(ctx).WithField("template", msg.Template).WithField("profile_id", msg.ProfileID)
	if cli == nil {
		return fmt.Errorf("notify: notification client is nil")
	}
	if msg.Template == "" {
		return fmt.Errorf("notify: template required")
	}
	if msg.ProfileID == "" {
		return fmt.Errorf("notify: profile_id required")
	}

	vars := msg.Variables
	if vars == nil {
		vars = map[string]any{}
	}
	payload, err := structpb.NewStruct(vars)
	if err != nil {
		return fmt.Errorf("notify: payload: %w", err)
	}

	priority := notificationv1.PRIORITY_LOW
	if msg.PrioritySet {
		priority = msg.Priority
	}

	// Same recipient shape as profile contact verification.
	recipient := &commonv1.ContactLink{
		ProfileType: "Profile",
		ProfileId:   msg.ProfileID,
		ContactId:   msg.ContactID,
	}

	n := &notificationv1.Notification{
		Recipient:   recipient,
		Payload:     payload,
		Language:    msg.Language,
		Template:    msg.Template,
		OutBound:    true,
		AutoRelease: true,
		Priority:    priority,
	}

	req := connect.NewRequest(&notificationv1.SendRequest{
		Data: []*notificationv1.Notification{n},
	})
	resp, err := cli.Send(ctx, req)
	if err != nil {
		log.WithError(err).Error("notify: NotificationService.Send failed")
		return err
	}
	if resp == nil {
		log.Debug("notify: Send response nil (test/degraded)")
		return nil
	}
	for resp.Receive() {
		if rerr := resp.Err(); rerr != nil {
			log.WithError(rerr).Error("notify: Send stream error")
			return rerr
		}
	}
	if closeErr := resp.Close(); closeErr != nil {
		log.WithError(closeErr).Warn("notify: close Send stream")
		return closeErr
	}
	log.Debug("notify: queued via service-notification")
	return nil
}

// ProfileID resolves the platform profile id for a candidate row.
// Falls back to candidateID when the row is missing or profile_id is empty
// (same COALESCE pattern used elsewhere for recipient binding).
func ProfileID(ctx context.Context, db *sql.DB, candidateID string) string {
	if candidateID == "" {
		return ""
	}
	if db == nil {
		return candidateID
	}
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(NULLIF(profile_id,''), id) FROM candidate_profiles WHERE id = $1`,
		candidateID,
	).Scan(&id)
	if err != nil || id == "" {
		return candidateID
	}
	return id
}

// OrDefault returns template if non-empty, otherwise def.
func OrDefault(template, def string) string {
	if template != "" {
		return template
	}
	return def
}
