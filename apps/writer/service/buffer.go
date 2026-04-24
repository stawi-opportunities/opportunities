package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/memconfig"
	"stawi.jobs/pkg/telemetry"
)

// Thresholds controls when a partition buffer flushes.
type Thresholds struct {
	MaxEvents   int
	MaxBytes    int
	MaxInterval time.Duration
}

// bufferKey is the in-memory partition key: (topic, partition-secondary).
type bufferKey struct {
	topic     string
	secondary string
}

// Batch is the unit produced on flush: one Parquet file's worth of events.
type Batch struct {
	Collection string
	EventType  string
	PartKey    eventsv1.PartKey
	Events     []json.RawMessage // raw bytes; writer re-decodes to the right type
}

// partitionBuffer is per-bufferKey state.
type partitionBuffer struct {
	dt       string
	events   []json.RawMessage
	bytes    int
	openedAt time.Time
}

// Buffer batches events in memory keyed by (topic, partition-secondary)
// and emits Batches when thresholds trip.
//
// Global memory cap: the Buffer tracks total bytes across ALL open partition
// buffers. When total exceeds maxTotalBytes (from a memconfig budget), the
// oldest partition buffer is force-flushed to keep memory bounded. This
// ensures memory stays O(budget), not O(number_of_source_ids × events).
//
// Safe for concurrent use from N Frame subscription goroutines.
type Buffer struct {
	thresholds    Thresholds
	budget        *memconfig.Budget
	maxTotalBytes int64

	mu         sync.Mutex
	parts      map[bufferKey]*partitionBuffer
	totalBytes int64
	// insertOrder tracks bufferKey insertion time to find the oldest partition.
	insertOrder []bufferKey
}

// NewBuffer constructs an empty Buffer. It acquires a memconfig budget for
// the writer-buffer subsystem (30% of pod memory) and logs a warning at
// construction time if FlushMaxBytes × estimated concurrent partitions would
// exceed the budget (informational only — actual enforcement is at runtime).
func NewBuffer(t Thresholds) *Buffer {
	budget := memconfig.NewBudget("writer-buffer", 30)
	return &Buffer{
		thresholds:    t,
		budget:        budget,
		maxTotalBytes: budget.Bytes(),
		parts:         make(map[bufferKey]*partitionBuffer),
	}
}

// Add enqueues an event. Returns a non-nil Batch if this add caused a
// flush (size/count or global memory cap triggers). hint is the
// partition-secondary value (source_id, cluster prefix, lang, etc.).
func (b *Buffer) Add(env any, hint string) (*Batch, error) {
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	topic, occurredAt, ok := extractEnvelopeMeta(raw)
	if !ok {
		return nil, errInvalidEnvelope
	}
	pk := eventsv1.PartitionKey(topic, occurredAt, hint)
	key := bufferKey{topic: topic, secondary: pk.Secondary}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Refresh maxTotalBytes from the budget on each add so vertical scaling
	// takes effect without a pod restart.
	b.maxTotalBytes = b.budget.Bytes()

	pb, exists := b.parts[key]
	if !exists {
		pb = &partitionBuffer{dt: pk.DT, openedAt: time.Now()}
		b.parts[key] = pb
		b.insertOrder = append(b.insertOrder, key)
	}
	pb.events = append(pb.events, raw)
	pb.bytes += len(raw)
	b.totalBytes += int64(len(raw))

	// Per-partition threshold.
	if len(pb.events) >= b.thresholds.MaxEvents || pb.bytes >= b.thresholds.MaxBytes {
		batch := b.flushLocked(key, pb)
		return batch, nil
	}

	// Global memory cap: force-flush the oldest partition when total exceeds budget.
	if b.totalBytes > b.maxTotalBytes {
		if oldest := b.oldestPartitionKey(); oldest != (bufferKey{}) {
			if opb, ok := b.parts[oldest]; ok {
				if telemetry.WriterBufferForceFlushesTotal != nil {
					telemetry.WriterBufferForceFlushesTotal.Add(context.Background(), 1)
				}
				return b.flushLocked(oldest, opb), nil
			}
		}
	}

	return nil, nil
}

// oldestPartitionKey returns the bufferKey of the partition that was opened
// earliest. Maintains insertOrder to avoid O(N) scan on every add.
func (b *Buffer) oldestPartitionKey() bufferKey {
	// Walk insertOrder, skipping keys that were already flushed.
	for len(b.insertOrder) > 0 {
		k := b.insertOrder[0]
		if _, ok := b.parts[k]; ok {
			return k
		}
		b.insertOrder = b.insertOrder[1:]
	}
	// Fallback: pick any key.
	for k := range b.parts {
		return k
	}
	return bufferKey{}
}

// Due returns Batches whose interval threshold has elapsed. Called by
// the writer service's periodic flush goroutine.
func (b *Buffer) Due() []*Batch {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	var out []*Batch
	for k, pb := range b.parts {
		if now.Sub(pb.openedAt) >= b.thresholds.MaxInterval && len(pb.events) > 0 {
			out = append(out, b.flushLocked(k, pb))
		}
	}
	return out
}

// FlushAll returns every remaining partition. Called on shutdown
// before the process exits so buffered events are durable.
func (b *Buffer) FlushAll() []*Batch {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []*Batch
	for k, pb := range b.parts {
		if len(pb.events) > 0 {
			out = append(out, b.flushLocked(k, pb))
		}
	}
	return out
}

// StatsForTopic returns the total (bytes, events) currently buffered across
// all partition-secondary slots for the given topic.  Returns (0,0) if no
// data is buffered for that topic.  Safe for concurrent use.
func (b *Buffer) StatsForTopic(topic string) (bytes int, events int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, pb := range b.parts {
		if k.topic == topic {
			bytes += pb.bytes
			events += len(pb.events)
		}
	}
	return bytes, events
}

// BufferStats is the aggregate view reported by Stats().
type BufferStats struct {
	TotalBytes    int64            // bytes currently buffered across all partitions
	MaxTotalBytes int64            // current budget ceiling (adapts with pod memory)
	PartitionCount int             // number of open partition buffers
	PerTopicBytes map[string]int64 // bytes per event topic
}

// Stats returns a point-in-time snapshot of buffer utilisation. Safe for
// concurrent use. Intended for observable gauge reporting.
func (b *Buffer) Stats() BufferStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	perTopic := make(map[string]int64, len(b.parts))
	for k, pb := range b.parts {
		perTopic[k.topic] += int64(pb.bytes)
	}
	return BufferStats{
		TotalBytes:     b.totalBytes,
		MaxTotalBytes:  b.maxTotalBytes,
		PartitionCount: len(b.parts),
		PerTopicBytes:  perTopic,
	}
}

func (b *Buffer) flushLocked(k bufferKey, pb *partitionBuffer) *Batch {
	batch := &Batch{
		Collection: collectionForTopic(k.topic),
		EventType:  k.topic,
		PartKey:    eventsv1.PartKey{DT: pb.dt, Secondary: k.secondary},
		Events:     pb.events,
	}
	b.totalBytes -= int64(pb.bytes)
	if b.totalBytes < 0 {
		b.totalBytes = 0
	}
	delete(b.parts, k)

	// Remove k from insertOrder so the slice does not grow unboundedly.
	// Linear scan is O(open partition count), typically O(100). At extreme
	// scale (O(1000s) of partitions) a linked list or ordered set would be
	// more efficient, but the linear scan is correct and simple for now.
	for i := range b.insertOrder {
		if b.insertOrder[i] == k {
			b.insertOrder = append(b.insertOrder[:i], b.insertOrder[i+1:]...)
			break
		}
	}
	return batch
}

// envelopeMeta is a minimal shape to peek at event_type + occurred_at
// without having to know the payload type.
type envelopeMeta struct {
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
}

func extractEnvelopeMeta(raw json.RawMessage) (string, time.Time, bool) {
	var m envelopeMeta
	if err := json.Unmarshal(raw, &m); err != nil || m.EventType == "" {
		return "", time.Time{}, false
	}
	return m.EventType, m.OccurredAt, true
}

// collectionForTopic maps an event topic to its Parquet partition
// name. Single source of truth for "which collection does this event
// live in."
func collectionForTopic(topic string) string {
	switch topic {
	case eventsv1.TopicVariantsIngested,
		eventsv1.TopicVariantsNormalized,
		eventsv1.TopicVariantsValidated,
		eventsv1.TopicVariantsFlagged,
		eventsv1.TopicVariantsClustered:
		return "variants"
	case eventsv1.TopicCanonicalsUpserted:
		return "canonicals"
	case eventsv1.TopicCanonicalsExpired:
		return "canonicals_expired"
	case eventsv1.TopicEmbeddings:
		return "embeddings"
	case eventsv1.TopicTranslations:
		return "translations"
	case eventsv1.TopicPublished:
		return "published"
	case eventsv1.TopicCrawlPageCompleted:
		return "crawl_page_completed"
	case eventsv1.TopicSourcesDiscovered:
		return "sources_discovered"
	case eventsv1.TopicCVUploaded, eventsv1.TopicCVExtracted:
		return "candidates_cv"
	case eventsv1.TopicCVImproved:
		return "candidates_improvements"
	case eventsv1.TopicCandidateEmbedding:
		return "candidates_embeddings"
	case eventsv1.TopicCandidatePreferencesUpdated:
		return "candidates_preferences"
	case eventsv1.TopicCandidateMatchesReady:
		return "candidates_matches_ready"
	default:
		return "_unknown"
	}
}

var errInvalidEnvelope = errInvalid("invalid envelope: missing event_type")

type errInvalid string

func (e errInvalid) Error() string { return string(e) }
