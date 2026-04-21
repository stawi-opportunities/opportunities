package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// Service is apps/writer's composition root.
type Service struct {
	svc      *frame.Service
	buffer   *Buffer
	uploader *eventlog.Uploader
	flushInt time.Duration
}

// NewService wires the Frame service + buffer + uploader. The caller
// is responsible for starting the Frame run-loop (usually via
// svc.Run in main.go).
func NewService(
	svc *frame.Service,
	buffer *Buffer,
	uploader *eventlog.Uploader,
	flushInterval time.Duration,
) *Service {
	return &Service{svc: svc, buffer: buffer, uploader: uploader, flushInt: flushInterval}
}

// RegisterSubscriptions wires one WriterHandler per topic into the
// Frame events manager. Called during startup, before svc.Run. Each
// handler is the Frame-dispatched entry point for that topic's
// messages.
func (s *Service) RegisterSubscriptions(topics []string) error {
	mgr := s.svc.EventsManager()
	if mgr == nil {
		return fmt.Errorf("writer: events manager unavailable — check NATS configuration")
	}
	for _, t := range topics {
		h := NewWriterHandler(t, s.buffer)
		mgr.Add(h)
	}
	return nil
}

// RunFlusher drives the time-based flush path. Size/count-based
// flushes happen inline in WriterHandler.Execute; this goroutine
// catches idle partitions whose MaxInterval has elapsed.
//
// Blocks until ctx is cancelled. On cancel it drains every remaining
// partition with FlushAll before returning so no buffered event is
// lost on graceful shutdown.
func (s *Service) RunFlusher(ctx context.Context) error {
	t := time.NewTicker(s.flushInt / 3)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return s.drain(context.Background())
		case <-t.C:
			for _, b := range s.buffer.Due() {
				if err := s.uploadBatch(ctx, b); err != nil {
					util.Log(ctx).WithError(err).
						WithField("collection", b.Collection).
						WithField("part", b.PartKey.Secondary).
						Error("writer: upload batch failed; keeping events for redelivery")
				}
			}
		}
	}
}

func (s *Service) drain(ctx context.Context) error {
	remaining := s.buffer.FlushAll()
	if len(remaining) == 0 {
		return nil
	}
	util.Log(ctx).WithField("batches", len(remaining)).Info("writer: draining on shutdown")
	for _, b := range remaining {
		if err := s.uploadBatch(ctx, b); err != nil {
			return fmt.Errorf("writer: drain: %w", err)
		}
	}
	return nil
}

// uploadBatch serializes the batch's events as Parquet and uploads
// to R2. For Phase 1 we dispatch only on VariantIngestedV1 since
// that's the only event type implemented. Phase 2 adds the other
// collections.
func (s *Service) uploadBatch(ctx context.Context, b *Batch) error {
	var body []byte
	var err error

	switch b.EventType {
	case eventsv1.TopicVariantsIngested:
		body, err = encodeBatch[eventsv1.VariantIngestedV1](b.Events)
	case eventsv1.TopicCanonicalsUpserted:
		body, err = encodeBatch[eventsv1.CanonicalUpsertedV1](b.Events)
	case eventsv1.TopicEmbeddings:
		body, err = encodeBatch[eventsv1.EmbeddingV1](b.Events)
	default:
		return fmt.Errorf("writer: no encoder registered for %q", b.EventType)
	}
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}

	objKey := b.PartKey.ObjectPath(b.Collection, xid.New().String())
	etag, err := s.uploader.Put(ctx, objKey, body)
	if err != nil {
		return fmt.Errorf("writer: put %q: %w", objKey, err)
	}
	util.Log(ctx).
		WithField("object", objKey).
		WithField("etag", etag).
		WithField("events", len(b.Events)).
		Info("writer: parquet flushed")
	return nil
}

// encodeBatch decodes each raw envelope into the typed payload and
// writes the resulting slice as Parquet. Parquet columns are
// generated from the P struct's `parquet` tags.
func encodeBatch[P any](raws []json.RawMessage) ([]byte, error) {
	rows := make([]P, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[P]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		rows = append(rows, env.Payload)
	}
	return eventlog.WriteParquet(rows)
}
