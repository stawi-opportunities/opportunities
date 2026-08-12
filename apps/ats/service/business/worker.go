package business

import (
	"context"
	"encoding/json"
	"time"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/repository"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

// OutboxWorker drains pending notification intents to service-notification.
type OutboxWorker struct {
	Outbox   repository.OutboxRepository
	Notify   notificationv1connect.NotificationServiceClient
	Template string
	// Interval between drain cycles.
	Interval time.Duration
	// Batch size per cycle.
	Batch int
}

// Run loops until ctx is cancelled. Safe to start once per process.
func (w *OutboxWorker) Run(ctx context.Context) {
	if w.Outbox == nil {
		return
	}
	interval := w.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	batch := w.Batch
	if batch <= 0 {
		batch = 25
	}
	log := util.Log(ctx)
	log.Info("ats: outbox worker started")
	t := time.NewTicker(interval)
	defer t.Stop()
	// Drain once immediately so book→email is fast without waiting a full tick.
	w.drain(ctx, batch)
	for {
		select {
		case <-ctx.Done():
			log.Info("ats: outbox worker stopped")
			return
		case <-t.C:
			w.drain(ctx, batch)
		}
	}
}

func (w *OutboxWorker) drain(ctx context.Context, batch int) {
	rows, err := w.Outbox.ListPending(ctx, batch)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("ats: outbox list pending")
		return
	}
	for _, msg := range rows {
		if err := w.deliver(ctx, msg); err != nil {
			attempts := msg.Attempts + 1
			_ = w.Outbox.MarkFailed(ctx, msg.ID, attempts)
			util.Log(ctx).WithError(err).WithField("outbox_id", msg.ID).
				WithField("attempts", attempts).Warn("ats: outbox deliver failed")
			continue
		}
		if err := w.Outbox.MarkSent(ctx, msg.ID); err != nil {
			util.Log(ctx).WithError(err).WithField("outbox_id", msg.ID).Warn("ats: outbox mark sent")
		}
	}
}

func (w *OutboxWorker) deliver(ctx context.Context, msg *models.OutboxMessage) error {
	if msg == nil {
		return nil
	}
	if w.Notify == nil {
		// No client configured: leave pending so a future deploy can drain.
		// Returning nil would mark sent and drop the intent.
		return errNotifyUnavailable
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(msg.PayloadJSON), &payload); err != nil {
		return err
	}
	profileID, _ := payload["profile_id"].(string)
	if profileID == "" {
		// Nothing to deliver; treat as done.
		return nil
	}
	tmpl := w.Template
	if tmpl == "" {
		tmpl = "template.opportunities.ats.interview.scheduled"
	}
	return notify.Send(ctx, w.Notify, notify.Message{
		Template:  tmpl,
		ProfileID: profileID,
		Variables: payload,
	})
}

type notifyUnavailableError struct{}

func (notifyUnavailableError) Error() string { return "ats: notification client not configured" }

var errNotifyUnavailable = notifyUnavailableError{}
