package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

// Embedder produces a dense vector for opportunity text. Optional —
// when nil, rows are stored without embeddings (search degrades to
// non-ANN filters).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// PostgresProcessor drains the durable queue directly into canonical serving
// tables.
type PostgresProcessor struct {
	store       *jobqueue.Store
	owner       string
	batch       int
	concurrency int
	poll        time.Duration
	lease       time.Duration
	maxAttempts int
	embedder    Embedder
}

func NewPostgresProcessor(store *jobqueue.Store, owner string, batch, concurrency int, poll, lease time.Duration, maxAttempts int) *PostgresProcessor {
	if owner == "" {
		owner = "worker-" + xid.New().String()
	}
	if batch <= 0 {
		batch = 100
	}
	if concurrency <= 0 {
		concurrency = 8
	}
	if poll <= 0 {
		poll = time.Second
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	return &PostgresProcessor{store: store, owner: owner, batch: batch, concurrency: concurrency, poll: poll, lease: lease, maxAttempts: maxAttempts}
}

// WithEmbedder enables post-complete vector writes for ANN search.
func (p *PostgresProcessor) WithEmbedder(e Embedder) *PostgresProcessor {
	p.embedder = e
	return p
}

func (p *PostgresProcessor) Run(ctx context.Context) {
	ticker := time.NewTicker(p.poll)
	defer ticker.Stop()
	for {
		if err := p.drain(ctx); err != nil {
			util.Log(ctx).WithError(err).Warn("postgres worker: drain failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *PostgresProcessor) drain(ctx context.Context) error {
	items, err := p.store.Claim(ctx, p.owner, p.batch, p.lease)
	if err != nil || len(items) == 0 {
		return err
	}
	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup
	for _, item := range items {
		item := item
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if processErr := p.process(ctx, item); processErr != nil {
				if retryErr := p.store.Retry(ctx, item, processErr, p.maxAttempts); retryErr != nil {
					util.Log(ctx).WithError(retryErr).WithField("ingest_id", item.ID).Error("postgres worker: retry persistence failed")
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

func (p *PostgresProcessor) process(ctx context.Context, item jobqueue.Item) error {
	var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
	if err := json.Unmarshal(item.Payload, &env); err != nil {
		return fmt.Errorf("decode variant: %w", err)
	}
	in := env.Payload
	if in.VariantID == "" || in.HardKey == "" || in.Title == "" {
		return fmt.Errorf("invalid variant payload")
	}
	n := Normalize(in)
	if n.ApplyURL == "" {
		return fmt.Errorf("invalid variant payload: apply_url is required")
	}
	a := n.Attributes
	c := jobqueue.Canonical{
		CandidateID: xid.New().String(), HardKey: in.HardKey, Kind: defaultString(in.Kind, "job"),
		SourceID: in.SourceID, ExternalID: in.ExternalID, Title: in.Title,
		Description: attrString(a, "description"), IssuingEntity: in.IssuingEntity,
		Country: in.AnchorCountry, Region: in.AnchorRegion, City: in.AnchorCity,
		ApplyURL: n.ApplyURL, Currency: in.Currency,
		EmploymentType: attrString(a, "employment_type"), Seniority: attrString(a, "seniority"),
		GeoScope: attrString(a, "geo_scope"), Remote: in.Remote || attrString(a, "remote_type") == "remote",
		AmountMin: in.AmountMin, AmountMax: in.AmountMax, SeenAt: in.ScrapedAt, Attributes: a,
	}
	c.PostedAt = attrTime(a, "posted_at")
	c.Deadline = attrTime(a, "deadline")
	canonicalID, err := p.store.Complete(ctx, item, c)
	if err != nil {
		return err
	}
	// Embed after the durable write so a slow/failed embedding API never
	// blocks or rolls back ingest. Fail-open: missing vectors leave the
	// row searchable by filters; embed-backfill heals historical NULLs.
	if p.embedder != nil && canonicalID != "" {
		text := extraction.EmbedInput(c.Title, c.IssuingEntity, c.Description)
		vec, embErr := p.embedder.Embed(ctx, text)
		if embErr != nil {
			util.Log(ctx).WithError(embErr).WithField("canonical_id", canonicalID).
				Warn("postgres worker: embed failed; row stored without vector")
			return nil
		}
		if len(vec) > 0 {
			if setErr := p.store.SetEmbedding(ctx, canonicalID, vec); setErr != nil {
				util.Log(ctx).WithError(setErr).WithField("canonical_id", canonicalID).
					Warn("postgres worker: set embedding failed")
			}
		}
	}
	return nil
}

func attrString(a map[string]any, key string) string {
	v := a[key]
	s, ok := v.(string)
	if ok {
		return s
	}
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return ""
}

func attrTime(a map[string]any, key string) *time.Time {
	s := attrString(a, key)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
