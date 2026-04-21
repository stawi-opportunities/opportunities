package service

import (
	"encoding/json"
	"sync"
	"time"

	eventsv1 "stawi.jobs/pkg/events/v1"
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
// Safe for concurrent use from N Frame subscription goroutines.
type Buffer struct {
	thresholds Thresholds

	mu    sync.Mutex
	parts map[bufferKey]*partitionBuffer
}

// NewBuffer constructs an empty Buffer.
func NewBuffer(t Thresholds) *Buffer {
	return &Buffer{
		thresholds: t,
		parts:      make(map[bufferKey]*partitionBuffer),
	}
}

// Add enqueues an event. Returns a non-nil Batch if this add caused a
// flush (size/count triggers), and the raw JSON bytes that were
// buffered. hint is the partition-secondary value (source_id, cluster
// prefix, lang, etc.).
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

	pb, ok := b.parts[key]
	if !ok {
		pb = &partitionBuffer{dt: pk.DT, openedAt: time.Now()}
		b.parts[key] = pb
	}
	pb.events = append(pb.events, raw)
	pb.bytes += len(raw)

	if len(pb.events) >= b.thresholds.MaxEvents || pb.bytes >= b.thresholds.MaxBytes {
		return b.flushLocked(key, pb), nil
	}
	return nil, nil
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

func (b *Buffer) flushLocked(k bufferKey, pb *partitionBuffer) *Batch {
	batch := &Batch{
		Collection: collectionForTopic(k.topic),
		EventType:  k.topic,
		PartKey:    eventsv1.PartKey{DT: pb.dt, Secondary: k.secondary},
		Events:     pb.events,
	}
	delete(b.parts, k)
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
	default:
		return "_unknown"
	}
}

var errInvalidEnvelope = errInvalid("invalid envelope: missing event_type")

type errInvalid string

func (e errInvalid) Error() string { return string(e) }
