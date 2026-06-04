package v1

import (
	"context"
	"encoding/json"
	"fmt"

	util "github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// CandidateEmbeddingIndexerDeps gathers the deps for the indexer consumer.
type CandidateEmbeddingIndexerDeps struct {
	IndexStore *matching.IndexStore
	DLQ        *DLQGuard
	// Name is the subscriber/durable name; must be DISTINCT from the
	// CandidateChangeConsumer's so both receive the event (fan-out).
	Name string
}

// CandidateEmbeddingIndexer auto-populates candidate_match_indexes from
// CandidateEmbeddingV1 events so the change/fan-out paths have an index row
// to read. It is a separate durable subscriber from CandidateChangeConsumer.
type CandidateEmbeddingIndexer struct {
	deps CandidateEmbeddingIndexerDeps
}

// NewCandidateEmbeddingIndexer wires the consumer.
func NewCandidateEmbeddingIndexer(d CandidateEmbeddingIndexerDeps) *CandidateEmbeddingIndexer {
	return &CandidateEmbeddingIndexer{deps: d}
}

// Name implements queue.SubscribeWorker.
func (c *CandidateEmbeddingIndexer) Name() string { return c.deps.Name }

// Handle implements queue.SubscribeWorker.
func (c *CandidateEmbeddingIndexer) Handle(ctx context.Context, headers map[string]string, payload []byte) error {
	redelivery := parseRedeliveryHeader(headers)
	return c.deps.DLQ.Run(ctx, redelivery, payload, func() error {
		return c.handleOnce(ctx, payload)
	})
}

func (c *CandidateEmbeddingIndexer) handleOnce(ctx context.Context, payload []byte) error {
	var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("matching: candidate embedding indexer decode: %w", err)
	}
	p := env.Payload
	if p.CandidateID == "" || len(p.Vector) == 0 {
		util.Log(ctx).WithField("candidate_id", p.CandidateID).
			Debug("candidate_embedding_indexer: empty candidate_id or vector; skip")
		return nil
	}
	if err := c.deps.IndexStore.Upsert(ctx, matching.CandidateIndex{
		CandidateID: p.CandidateID,
		Embedding:   p.Vector,
		MinScore:    0.5,
		DailyCap:    25,
		WeeklyCap:   100,
		Kinds:       []string{"job"},
		Enabled:     true,
	}); err != nil {
		return fmt.Errorf("matching: candidate embedding indexer upsert %s: %w", p.CandidateID, err)
	}
	util.Log(ctx).WithField("candidate_id", p.CandidateID).
		Debug("candidate_embedding_indexer: index row upserted")
	return nil
}
