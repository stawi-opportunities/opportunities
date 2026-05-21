package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	util "github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// CandidateChangeConsumerDeps gathers the deps for one consumer.
type CandidateChangeConsumerDeps struct {
	IndexStore *matching.IndexStore
	KNN        *matching.KNN
	Store      *matching.Store
	EventLog   *matching.EventLog
	Reranker   matching.Reranker
	Weights    matching.Weights
	Debouncer  matching.Debouncer
	DLQ        *DLQGuard
	Topic      string // TopicCandidatePreferencesUpdated or TopicCandidateEmbedding
}

// CandidateChangeConsumer subscribes to one trigger topic and runs Path C.
type CandidateChangeConsumer struct {
	deps CandidateChangeConsumerDeps
}

// NewCandidateChangeConsumer wires the consumer.
func NewCandidateChangeConsumer(d CandidateChangeConsumerDeps) *CandidateChangeConsumer {
	return &CandidateChangeConsumer{deps: d}
}

// Name implements queue.SubscribeWorker.
func (c *CandidateChangeConsumer) Name() string { return c.deps.Topic }

// Handle implements queue.SubscribeWorker. Frame surfaces JetStream
// metadata (including redelivery count) via the headers map.
func (c *CandidateChangeConsumer) Handle(ctx context.Context, headers map[string]string, payload []byte) error {
	redelivery := parseRedeliveryHeader(headers)
	return c.deps.DLQ.Run(ctx, redelivery, payload, func() error {
		return c.handleOnce(ctx, payload)
	})
}

func (c *CandidateChangeConsumer) handleOnce(ctx context.Context, payload []byte) error {
	candidateID, triggeredBy, err := decodeCandidateChange(c.deps.Topic, payload)
	if err != nil {
		return err
	}
	idx, err := c.deps.IndexStore.Get(ctx, candidateID)
	if err != nil {
		if errors.Is(err, matching.ErrNotFound) {
			util.Log(ctx).WithField("candidate_id", candidateID).
				Info("candidate_change: no index row yet; skip")
			return nil
		}
		return fmt.Errorf("matching: candidate change load index %s: %w", candidateID, err)
	}
	_, err = matching.RunCandidateChange(ctx, matching.CandidateChange{
		CandidateID:    candidateID,
		Embedding:      idx.Embedding,
		Countries:      idx.Countries,
		Kinds:          idx.Kinds,
		SalaryFloorUSD: idx.SalaryFloorUSD,
		MinScore:       idx.MinScore,
		TriggeredBy:    triggeredBy,
	}, matching.CandidateChangeDeps{
		Debouncer: c.deps.Debouncer,
		GapFill: matching.GapFillDeps{
			KNN:      c.deps.KNN,
			Store:    c.deps.Store,
			EventLog: c.deps.EventLog,
			Reranker: c.deps.Reranker,
			Weights:  c.deps.Weights,
		},
	})
	if errors.Is(err, matching.ErrDebounced) {
		util.Log(ctx).WithField("candidate_id", candidateID).
			Info("candidate_change: debounced")
		return nil
	}
	return err
}

// decodeCandidateChange extracts the candidate_id and a TriggeredBy
// label from the topic-specific payload shape.
// Both event types (PreferencesUpdatedV1, CandidateEmbeddingV1) use the
// JSON field name "candidate_id" — confirmed from pkg/events/v1/candidates.go.
func decodeCandidateChange(topic string, payload []byte) (candidateID, triggeredBy string, err error) {
	switch topic {
	case eventsv1.TopicCandidatePreferencesUpdated:
		var env eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
		if err = json.Unmarshal(payload, &env); err != nil {
			return "", "", fmt.Errorf("matching: candidate change decode prefs: %w", err)
		}
		return env.Payload.CandidateID, "rules_changed", nil
	case eventsv1.TopicCandidateEmbedding:
		var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
		if err = json.Unmarshal(payload, &env); err != nil {
			return "", "", fmt.Errorf("matching: candidate change decode embed: %w", err)
		}
		return env.Payload.CandidateID, "cv_changed", nil
	}
	return "", "", fmt.Errorf("matching: candidate change unknown topic %q", topic)
}
