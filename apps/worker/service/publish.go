package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/queue"
	"github.com/pitabwire/util"
	"golang.org/x/sync/singleflight"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// PublishHandler consumes CanonicalUpsertedV1 and writes a JSON
// snapshot to R2, then emits PublishedV1.
type PublishHandler struct {
	svc       *frame.Service
	publisher *publish.R2Publisher
	registry  *opportunity.Registry
	store     *variantstate.Store // nil-safe; soft-fails on Postgres outage

	// flight collapses concurrent work for the SAME object key into a
	// single execution. A canonical fans in from many variants, and the
	// ack-on-failure + reaper re-drive + NATS redelivery paths routinely
	// put several copies of one canonical in flight at once. Without this,
	// they race concurrent PUTs to the same R2 key → R2 rejects with 429
	// "Reduce your concurrent request rate for the same object" → every
	// copy then runs the heavy RecordErrorByCanonical jsonb UPDATE → an
	// I/O storm that wedges publish at 0 successes. Keyed by object key so
	// distinct jobs still publish fully in parallel; only duplicate
	// same-key work (which is pure waste) is serialized.
	flight singleflight.Group

	next string // Queue Name for PublishedV1 (consumed by the writer/archival)
}

// NewPublishHandler binds the handler. next is the published Queue Name.
func NewPublishHandler(svc *frame.Service, p *publish.R2Publisher, reg *opportunity.Registry, store *variantstate.Store, next string) *PublishHandler {
	return &PublishHandler{svc: svc, publisher: p, registry: reg, store: store, next: next}
}

var _ queue.SubscribeWorker = (*PublishHandler)(nil)

// Handle writes the R2 snapshot and publishes PublishedV1.
func (h *PublishHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if h.publisher == nil {
		return nil // publisher not configured — skip
	}
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return err
	}
	c := env.Payload

	snap, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("publish: marshal: %w", err)
	}
	spec := h.registry.Resolve(c.Kind)
	key := publish.ObjectKey(spec.URLPrefix, c.Slug)

	// Collapse concurrent same-key work (see flight field). The shared
	// function performs the upload, emit, and ledger advance exactly once
	// for all in-flight copies of this key; every caller observes the same
	// outcome and ACKs. We always return nil to NATS — failures are
	// recorded on the ledger and the reaper re-drives, never Nacked (a
	// Nack on the shared events consumer starves every other stage).
	_, _, _ = h.flight.Do(key, func() (any, error) {
		if err := h.publisher.UploadPublicSnapshot(ctx, key, snap); err != nil {
			// CRITICAL: R2 publish failures must NOT Nack the shared events
			// consumer. All five pipeline stages multiplex onto ONE NATS
			// consumer with no app-level max-deliver/DLQ, so a Nack-storm
			// back-pressures and starves every other stage (the ~18h "0
			// published" incident). Log WARN, record the error against the
			// canonical's variants, and ACK. Variants stay at `canonical`;
			// the reaper re-drives once R2 is healthy.
			wrapped := fmt.Errorf("publish: upload: %w", err)
			util.Log(ctx).WithError(wrapped).
				WithField("canonical_id", c.OpportunityID).
				WithField("key", key).
				Warn("publish: R2 upload failed; acking to protect shared consumer, reaper will re-drive")
			_ = h.store.RecordErrorByCanonical(ctx, c.OpportunityID, variantstate.StageCanonical, wrapped)
			return nil, nil
		}

		out := eventsv1.PublishedV1{
			OpportunityID: c.OpportunityID,
			Slug:          c.Slug,
			Kind:          c.Kind,
			R2Version:     int(time.Now().Unix()),
			PublishedAt:   time.Now().UTC(),
		}
		if body, mErr := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicPublished, out)); mErr == nil {
			if pErr := h.svc.QueueManager().Publish(ctx, h.next, body, nil); pErr != nil {
				// Snapshot is up; log and still advance the ledger so we don't
				// re-drive a successful upload.
				util.Log(ctx).WithError(pErr).
					WithField("canonical_id", c.OpportunityID).
					Warn("publish: publish PublishedV1 failed after successful upload")
			}
		}
		// Bulk-advance every variant in this canonical from `canonical`
		// → `published`. CanonicalUpsertedV1 is a many-to-one fan-in
		// (multiple variants share a canonical), so update by canonical_id.
		_ = h.store.MarkPublishedByCanonical(ctx, c.OpportunityID)
		return nil, nil
	})
	return nil
}
