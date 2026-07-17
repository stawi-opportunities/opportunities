// Package notify delivers candidate-facing messages exclusively through
// the platform notification service (service-notification). Matching never
// sends email/SMS itself — it always queues via NotificationService.Send.
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	commonv1 "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
	"github.com/pitabwire/util"
)

// Template names registered in service-notification for opportunities.
const (
	TemplateMatchesReady     = "matches_ready"      // every-match alert (opt-in)
	TemplateMatchesDigest    = "matches_digest"     // paid subscriber summary
	TemplateWeeklyJobsDigest = "weekly_jobs_digest" // unpaid re-engagement
	TemplateCVStaleNudge     = "cv_stale_nudge"
)

// Notifier queues outbound notifications via service-notification.
// Nil client degrades to no-ops (logged) so matching still boots without it.
type Notifier struct {
	Client        notificationv1connect.NotificationServiceClient
	DB            *sql.DB // optional: resolve profile_id for recipients
	PublicSiteURL string
}

// New builds a Notifier. client may be nil (degraded).
func New(client notificationv1connect.NotificationServiceClient, db *sql.DB, publicSiteURL string) *Notifier {
	return &Notifier{Client: client, DB: db, PublicSiteURL: strings.TrimRight(publicSiteURL, "/")}
}

// MatchItem is one opportunity in a match notification payload.
type MatchItem struct {
	CanonicalID string  `json:"canonical_id"`
	Title       string  `json:"title,omitempty"`
	Company     string  `json:"company,omitempty"`
	ApplyURL    string  `json:"apply_url,omitempty"`
	Slug        string  `json:"slug,omitempty"`
	Score       float64 `json:"score,omitempty"`
}

// MatchesReady queues a match notification for one candidate.
// When immediate is false the payload still goes to the notification
// service with delivery=digest so templates/routing can no-op or batch;
// when true (match_alerts) delivery=immediate for real-time send.
func (n *Notifier) MatchesReady(ctx context.Context, candidateID, batchID string, items []MatchItem, immediate bool) {
	if len(items) == 0 {
		return
	}
	delivery := "digest"
	priority := notificationv1.PRIORITY_LOW
	if immediate {
		delivery = "immediate"
		priority = notificationv1.PRIORITY_HIGH
	}
	data := map[string]any{
		"candidate_id":   candidateID,
		"match_batch_id": batchID,
		"matches":        items,
		"delivery":       delivery,
		"dashboard_url":  n.PublicSiteURL + "/dashboard/#matches",
		"count":          len(items),
	}
	n.send(ctx, TemplateMatchesReady, "matches", candidateID, data, priority)
}

// MatchesDigest queues the paid-subscriber weekly/daily summary.
func (n *Notifier) MatchesDigest(ctx context.Context, candidateID, batchID string, items []MatchItem) {
	if len(items) == 0 {
		return
	}
	data := map[string]any{
		"candidate_id":   candidateID,
		"match_batch_id": batchID,
		"matches":        items,
		"delivery":       "digest",
		"dashboard_url":  n.PublicSiteURL + "/dashboard/#matches",
		"count":          len(items),
	}
	n.send(ctx, TemplateMatchesDigest, "matches_digest", candidateID, data, notificationv1.PRIORITY_LOW)
}

// WeeklyJobsDigest queues the unpaid re-engagement summary.
func (n *Notifier) WeeklyJobsDigest(ctx context.Context, candidateID string, payload any) {
	data := map[string]any{
		"candidate_id": candidateID,
		"payload":      payload,
		"delivery":     "digest",
		"plans_url":    n.PublicSiteURL + "/pricing/",
	}
	n.send(ctx, TemplateWeeklyJobsDigest, "jobs_digest", candidateID, data, notificationv1.PRIORITY_LOW)
}

// CVStaleNudge queues a CV refresh reminder.
func (n *Notifier) CVStaleNudge(ctx context.Context, candidateID string, payload any) {
	data := map[string]any{
		"candidate_id":  candidateID,
		"payload":       payload,
		"dashboard_url": n.PublicSiteURL + "/dashboard/#preferences",
	}
	n.send(ctx, TemplateCVStaleNudge, "cv_nudge", candidateID, data, notificationv1.PRIORITY_LOW)
}

func (n *Notifier) send(
	ctx context.Context,
	template, nType, candidateID string,
	data map[string]any,
	priority notificationv1.PRIORITY,
) {
	log := util.Log(ctx).WithField("template", template).WithField("candidate_id", candidateID)
	if n == nil || n.Client == nil {
		log.Warn("notify: notification client nil — message not delivered via service-notification")
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		log.WithError(err).Warn("notify: marshal data failed")
		return
	}
	profileID := n.resolveProfileID(ctx, candidateID)
	notif := notificationv1.Notification_builder{
		Type:     nType,
		Template: template,
		Data:     string(raw),
		Recipient: commonv1.ContactLink_builder{
			ProfileId: profileID,
		}.Build(),
		OutBound:    true,
		AutoRelease: true,
		Priority:    priority,
	}.Build()

	stream, err := n.Client.Send(ctx, connect.NewRequest(&notificationv1.SendRequest{
		Data: []*notificationv1.Notification{notif},
	}))
	if err != nil {
		log.WithError(err).Error("notify: NotificationService.Send failed")
		return
	}
	for stream.Receive() {
		// drain
	}
	if closeErr := stream.Close(); closeErr != nil {
		log.WithError(closeErr).Warn("notify: close send stream")
	} else {
		log.Debug("notify: queued via service-notification")
	}
}

func (n *Notifier) resolveProfileID(ctx context.Context, candidateID string) string {
	if n.DB == nil || candidateID == "" {
		return candidateID
	}
	var profileID string
	err := n.DB.QueryRowContext(ctx,
		`SELECT COALESCE(NULLIF(profile_id,''), id) FROM candidate_profiles WHERE id = $1`,
		candidateID,
	).Scan(&profileID)
	if err != nil || profileID == "" {
		return candidateID
	}
	return profileID
}
