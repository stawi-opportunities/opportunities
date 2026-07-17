package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

// StaleCandidate is one candidate whose most recent CV upload is older
// than the stale threshold.
type StaleCandidate struct {
	CandidateID  string
	LastUploadAt time.Time
}

// StaleLister enumerates candidate state stored in PostgreSQL.
type StaleLister interface {
	ListStale(ctx context.Context, asOf time.Time) ([]StaleCandidate, error)
}

// CVStaleNudgeDeps bundles collaborators.
type CVStaleNudgeDeps struct {
	Svc        *frame.Service
	Lister     StaleLister
	StaleAfter time.Duration // default 60 days
	// NotificationCli is the platform notification service client.
	NotificationCli notificationv1connect.NotificationServiceClient
	Templates       notify.Templates
	ProfileID       func(ctx context.Context, candidateID string) string
	PublicSiteURL   string
}

type cvStaleNudgeResponse struct {
	OK      bool `json:"ok"`
	Emitted int  `json:"emitted"`
}

// cvStaleNudgePayload is the body of the TopicCandidateCVStaleNudge event.
type cvStaleNudgePayload struct {
	CandidateID     string    `json:"candidate_id"`
	LastUploadAt    time.Time `json:"last_upload_at"`
	DaysSinceUpload int       `json:"days_since_upload"`
}

// CVStaleNudgeHandler returns an http.HandlerFunc fired by Trustage
// daily. It queues one CV-stale-nudge notification per candidate whose
// most recent upload is older than StaleAfter (via NotificationService.Send).
func CVStaleNudgeHandler(deps CVStaleNudgeDeps) http.HandlerFunc {
	staleAfter := deps.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 60 * 24 * time.Hour
	}
	site := strings.TrimRight(deps.PublicSiteURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		now := time.Now().UTC()
		candidates, err := deps.Lister.ListStale(ctx, now.Add(-staleAfter))
		if err != nil {
			log.WithError(err).Error("stale-nudge: ListStale failed")
			http.Error(w, `{"error":"list stale failed"}`, http.StatusInternalServerError)
			return
		}

		resp := cvStaleNudgeResponse{OK: true}
		for _, c := range candidates {
			days := int(now.Sub(c.LastUploadAt).Hours() / 24)
			payload := cvStaleNudgePayload{
				CandidateID:     c.CandidateID,
				LastUploadAt:    c.LastUploadAt,
				DaysSinceUpload: days,
			}
			if deps.NotificationCli != nil {
				profileID := c.CandidateID
				if deps.ProfileID != nil {
					profileID = deps.ProfileID(ctx, c.CandidateID)
				}
				_ = notify.Send(ctx, deps.NotificationCli, notify.Message{
					Template:  deps.Templates.CVStale(),
					ProfileID: profileID,
					Variables: map[string]any{
						"candidate_id":      c.CandidateID,
						"days_since_upload": float64(days),
						"last_upload_at":    c.LastUploadAt.Format(time.RFC3339),
						"dashboard_url":     site + "/dashboard/#preferences",
					},
				})
			} else {
				log.WithField("candidate_id", c.CandidateID).
					Warn("stale-nudge: notification client nil — not queued")
			}
			if deps.Svc != nil {
				env := eventsv1.NewEnvelope(eventsv1.TopicCandidateCVStaleNudge, payload)
				if err := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateCVStaleNudge, env); err != nil {
					log.WithError(err).WithField("candidate_id", c.CandidateID).
						Debug("stale-nudge: domain event emit failed")
				}
			}
			resp.Emitted++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
