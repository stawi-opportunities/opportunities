package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// StaleCandidate is one candidate whose most recent CV upload is older
// than the stale threshold.
type StaleCandidate struct {
	CandidateID  string
	LastUploadAt time.Time
}

// StaleLister enumerates stale candidates. Real impl scans
// candidates_cv_current/ in R2 and filters by occurred_at < cutoff.
// v1 uses a Postgres last-upload column as a shortcut.
type StaleLister interface {
	ListStale(ctx context.Context, asOf time.Time) ([]StaleCandidate, error)
}

// CVStaleNudgeDeps bundles collaborators.
type CVStaleNudgeDeps struct {
	Svc        *frame.Service
	Lister     StaleLister
	StaleAfter time.Duration // default 60 days
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
// daily. It emits one CV-stale-nudge event per candidate whose most
// recent upload is older than StaleAfter. The external notification
// service consumes the topic and sends the nudge email.
func CVStaleNudgeHandler(deps CVStaleNudgeDeps) http.HandlerFunc {
	staleAfter := deps.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 60 * 24 * time.Hour
	}
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
			env := eventsv1.NewEnvelope(eventsv1.TopicCandidateCVStaleNudge, cvStaleNudgePayload{
				CandidateID:     c.CandidateID,
				LastUploadAt:    c.LastUploadAt,
				DaysSinceUpload: days,
			})
			if err := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateCVStaleNudge, env); err != nil {
				log.WithError(err).WithField("candidate_id", c.CandidateID).Warn("stale-nudge: emit failed")
				continue
			}
			resp.Emitted++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
