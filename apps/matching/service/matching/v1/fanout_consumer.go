package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	util "github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// FanOutConsumerDeps gathers everything the consumer needs at construction.
type FanOutConsumerDeps struct {
	Store     *matching.Store
	EventLog  *matching.EventLog
	KNN       *matching.KNN
	Reranker  matching.Reranker
	Weights   matching.Weights
	DLQ       *DLQGuard
	OppEmbedQ OppEmbeddingQuery
}

// OppEmbeddingQuery hides the opportunity-embedding fetch from the
// consumer so the integration test can stub it without touching variantstate.
type OppEmbeddingQuery interface {
	GetEmbedding(ctx context.Context, opportunityID string) ([]float32, error)
}

// FanOutConsumer wires Path A to TopicCanonicalsUpserted.
type FanOutConsumer struct {
	deps FanOutConsumerDeps
}

// NewFanOutConsumer wires the consumer.
func NewFanOutConsumer(d FanOutConsumerDeps) *FanOutConsumer {
	return &FanOutConsumer{deps: d}
}

// Name implements queue.SubscribeWorker.
func (c *FanOutConsumer) Name() string { return eventsv1.TopicCanonicalsUpserted }

// Handle implements queue.SubscribeWorker. Frame surfaces JetStream
// metadata (including redelivery count) via the headers map.
func (c *FanOutConsumer) Handle(ctx context.Context, headers map[string]string, payload []byte) error {
	redelivery := parseRedeliveryHeader(headers)
	return c.deps.DLQ.Run(ctx, redelivery, payload, func() error {
		return c.handleOnce(ctx, payload)
	})
}

func (c *FanOutConsumer) handleOnce(ctx context.Context, payload []byte) error {
	// Envelope[T] is generic; unmarshal into it with the correct field name.
	// The envelope uses "payload" (json tag) for the typed body.
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("matching: fanout decode: %w", err)
	}
	body := env.Payload

	embedding, err := c.deps.OppEmbedQ.GetEmbedding(ctx, body.OpportunityID)
	if err != nil {
		return fmt.Errorf("matching: load opp embedding %s: %w", body.OpportunityID, err)
	}
	if len(embedding) == 0 {
		util.Log(ctx).WithField("opportunity_id", body.OpportunityID).
			Info("fanout: opp has no embedding; skipping")
		return nil
	}

	var salaryMax *int
	if body.AmountMax > 0 {
		v := int(body.AmountMax)
		salaryMax = &v
	}
	var skills []string
	if cats, ok := body.Attributes["skills"].([]string); ok {
		skills = cats
	}

	_, err = matching.FanOut(ctx, matching.FanOutInput{
		CanonicalID:   body.HardKey,
		OpportunityID: body.OpportunityID,
		Kind:          body.Kind,
		Country:       body.AnchorCountry,
		SalaryMaxUSD:  salaryMax,
		Embedding:     embedding,
		FirstSeenAt:   body.UpsertedAt,
		Skills:        skills,
	}, matching.FanOutDeps{
		KNN:      c.deps.KNN,
		Store:    c.deps.Store,
		EventLog: c.deps.EventLog,
		Reranker: c.deps.Reranker,
		Weights:  c.deps.Weights,
	})
	return err
}

// parseRedeliveryHeader extracts the NATS JetStream redelivery count from
// Frame's header map. JetStream sets "Nats-Redelivery-Count" on redeliveries.
func parseRedeliveryHeader(headers map[string]string) int {
	for _, key := range []string{"Nats-Redelivery-Count", "nats-redelivery-count", "redelivery"} {
		if v, ok := headers[key]; ok {
			var n int
			_, _ = fmt.Sscanf(v, "%d", &n)
			return n
		}
	}
	return 0
}

// SQLOppEmbeddingQuery reads the opportunity's embedding column directly.
type SQLOppEmbeddingQuery struct {
	db *sql.DB
}

// NewSQLOppEmbeddingQuery constructs a query adapter backed by *sql.DB.
func NewSQLOppEmbeddingQuery(db *sql.DB) *SQLOppEmbeddingQuery {
	return &SQLOppEmbeddingQuery{db: db}
}

// GetEmbedding returns the pgvector embedding stored for opportunityID.
// Returns nil (not an error) when no row exists or the column is NULL/empty.
func (q *SQLOppEmbeddingQuery) GetEmbedding(ctx context.Context, opportunityID string) ([]float32, error) {
	var text string
	err := q.db.QueryRowContext(ctx,
		`SELECT COALESCE(embedding::text, '') FROM opportunities WHERE id = $1`, opportunityID).
		Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	return matching.ParseVectorLiteral(text), nil
}
