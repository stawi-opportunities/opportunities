package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// Service is apps/writer's composition root.
type Service struct {
	svc      *frame.Service
	buffer   *Buffer
	catalog  catalog.Catalog
	pool     memory.Allocator
	flushInt time.Duration
}

// NewService wires the Frame service + buffer + Iceberg catalog.
// The caller is responsible for starting the Frame run-loop
// (usually via svc.Run in main.go).
func NewService(
	svc *frame.Service,
	buffer *Buffer,
	cat catalog.Catalog,
	flushInterval time.Duration,
) *Service {
	return &Service{
		svc:      svc,
		buffer:   buffer,
		catalog:  cat,
		pool:     memory.NewGoAllocator(),
		flushInt: flushInterval,
	}
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
				if err := s.commitBatch(ctx, b); err != nil {
					util.Log(ctx).WithError(err).
						WithField("collection", b.Collection).
						WithField("part", b.PartKey.Secondary).
						Error("writer: commit batch failed; keeping events for redelivery")
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
		if err := s.commitBatch(ctx, b); err != nil {
			return fmt.Errorf("writer: drain: %w", err)
		}
	}
	return nil
}

// commitBatch builds an Arrow RecordReader from the raw events and
// commits via Transaction.Append. Transient catalog failures (including
// OCC conflicts from the second writer replica) are retried by
// CommitBatchWithRetry, which reloads the table on each attempt so
// stale metadata never loops forever.
//
// Topics that are no longer persisted to Iceberg (canonicals, translations,
// canonicals_expired) return a nil builder; those batches are acked without
// writing to Iceberg — Frame fan-out still delivers them to the materializer's
// Frame subscriber.
func (s *Service) commitBatch(ctx context.Context, b *Batch) error {
	tableIdent, builder := batchDispatch(b.EventType)
	if builder == nil {
		// Recognised topic but not persisted to Iceberg — ack and move on.
		util.Log(ctx).
			WithField("topic", b.EventType).
			Debug("writer: topic acked without Iceberg commit (R2-only or Frame-subscriber path)")
		return nil
	}
	_, err := CommitBatchWithRetry(ctx, s.catalog, tableIdent, func() (array.RecordReader, error) {
		return builder(s.pool, b.Events)
	}, 5)
	if err != nil {
		return fmt.Errorf("writer: commit %v: %w", tableIdent, err)
	}
	util.Log(ctx).
		WithField("table", tableIdent).
		WithField("events", len(b.Events)).
		Info("iceberg commit ok")
	return nil
}

// batchDispatch maps a Frame topic to the corresponding Iceberg table
// identifier and Arrow record builder function.
//
// VariantNormalized / VariantValidated / VariantFlagged / VariantClustered
// all route to jobs.variants — _schemas.py defines a single VARIANTS
// schema for all pipeline stages, distinguished by the "stage" field.
//
// Topics that are NO LONGER persisted to Iceberg return (nil, nil):
//   - TopicCanonicalsUpserted  — body lives at s3://opportunities-content/jobs/<slug>.json
//   - TopicCanonicalsExpired   — Frame event only; materializer subscribes directly
//   - TopicTranslations        — body lives at s3://opportunities-content/jobs/<slug>/<lang>.json
//
// The writer still subscribes to these topics (they remain in AllTopics()); the
// buffer receives them, commitBatch sees a nil builder, and acks without writing.
func batchDispatch(topic string) ([]string, func(memory.Allocator, []json.RawMessage) (array.RecordReader, error)) {
	switch topic {
	case eventsv1.TopicVariantsIngested:
		return []string{"opportunities", "variants"}, BuildVariantIngestedRecord
	case eventsv1.TopicVariantsNormalized:
		return []string{"opportunities", "variants"}, BuildVariantNormalizedRecord
	case eventsv1.TopicVariantsValidated:
		return []string{"opportunities", "variants"}, BuildVariantValidatedRecord
	case eventsv1.TopicVariantsFlagged:
		return []string{"opportunities", "variants"}, BuildVariantFlaggedRecord
	case eventsv1.TopicVariantsClustered:
		return []string{"opportunities", "variants"}, BuildVariantClusteredRecord
	// TopicCanonicalsUpserted, TopicCanonicalsExpired, TopicTranslations
	// intentionally omitted — body is R2-slug-direct; materializer is a
	// Frame subscriber, not an Iceberg scanner for these topics.
	case eventsv1.TopicEmbeddings:
		return []string{"opportunities", "embeddings"}, BuildEmbeddingRecord
	case eventsv1.TopicPublished:
		return []string{"opportunities", "published"}, BuildPublishedRecord
	case eventsv1.TopicCrawlPageCompleted:
		return []string{"opportunities", "crawl_page_completed"}, BuildCrawlPageCompletedRecord
	case eventsv1.TopicSourcesDiscovered:
		return []string{"opportunities", "sources_discovered"}, BuildSourceDiscoveredRecord
	case eventsv1.TopicCVUploaded:
		return []string{"candidates", "cv_uploaded"}, BuildCVUploadedRecord
	case eventsv1.TopicCVExtracted:
		return []string{"candidates", "cv_extracted"}, BuildCVExtractedRecord
	case eventsv1.TopicCVImproved:
		return []string{"candidates", "cv_improved"}, BuildCVImprovedRecord
	case eventsv1.TopicCandidateEmbedding:
		return []string{"candidates", "embeddings"}, BuildCandidateEmbeddingRecord
	case eventsv1.TopicCandidatePreferencesUpdated:
		return []string{"candidates", "preferences"}, BuildPreferencesRecord
	case eventsv1.TopicCandidateMatchesReady:
		return []string{"candidates", "matches_ready"}, BuildMatchesReadyRecord
	default:
		return nil, nil
	}
}
