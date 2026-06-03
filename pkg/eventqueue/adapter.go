// Package eventqueue bridges the worker's existing events.EventI pipeline
// handlers onto Frame Queue subjects.
//
// Inter-service hops (crawler→normalize→validate→dedup→canonical→publish,
// and the canonical fan-out to materializer/matching/writer) are durable,
// cross-service, must-survive-restart work — which Frame's async decision
// tree places on Frame Queue (dedicated subject + per-consumer durable),
// not the shared Frame Events bus. The events bus routes every event to
// every consumer by an in-band header on one subject, so each stage group
// receives (and loose-mode-acks) every other stage's events — the
// catch-all cross-talk behind the "event not registered" noise and the
// shared-stream backlog.
//
// AsQueueWorker lets us move a hop onto its own Queue subject without
// rewriting the handler: the existing EventI keeps its Validate/Execute
// logic and we drive it from a queue.SubscribeWorker instead of the
// events manager.
package eventqueue

import (
	"context"
	"encoding/json"

	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/queue"
)

// queueWorker adapts an events.EventI to a queue.SubscribeWorker.
type queueWorker struct{ h events.EventI }

// AsQueueWorker wraps an events.EventI pipeline handler so it can be
// registered with frame.WithRegisterSubscriber on a dedicated Queue
// subject. The handler's PayloadType/Validate/Execute contract is
// preserved exactly — only the transport (Queue subject instead of the
// shared events stream) changes.
func AsQueueWorker(h events.EventI) queue.SubscribeWorker { return queueWorker{h: h} }

// Handle implements queue.SubscribeWorker. It reconstructs the handler's
// declared payload type from the raw queue message, runs Validate, then
// Execute — mirroring how events.Manager drives EventI handlers. A
// returned error makes the queue redeliver (durable retry), which is the
// whole point of moving these hops onto Queue.
func (w queueWorker) Handle(ctx context.Context, _ map[string]string, message []byte) error {
	p := w.h.PayloadType()
	if err := json.Unmarshal(message, p); err != nil {
		return err
	}
	if err := w.h.Validate(ctx, p); err != nil {
		return err
	}
	return w.h.Execute(ctx, p)
}
