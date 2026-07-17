package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

// EmbedPublisher publishes durable embed jobs onto Frame Queue.
// Implemented by *frame.Service via QueueManager().Publish.
type EmbedPublisher interface {
	PublishEmbed(ctx context.Context, job eventsv1.OpportunityEmbedV1) error
}

// PostgresProcessor drains the durable PostgreSQL job_ingest_queue into
// canonical serving tables.
//
// Lifecycle: Run is intended as a Frame WithBackgroundConsumer — Frame
// owns the goroutine and ties exit to service shutdown. Concurrent item
// processing uses the Frame workerpool (ants), not ad-hoc goroutines.
// Embedding is NOT done inline (external HTTP): after Complete, an
// OpportunityEmbedV1 is published to SubjectWorkerEmbed for the Queue
// subscriber.
type PostgresProcessor struct {
	store       *jobqueue.Store
	svc         *frame.Service
	owner       string
	batch       int
	concurrency int
	poll        time.Duration
	lease       time.Duration
	maxAttempts int
	embeds      EmbedPublisher
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

// WithService wires Frame's workerpool for concurrent drain.
func (p *PostgresProcessor) WithService(svc *frame.Service) *PostgresProcessor {
	p.svc = svc
	return p
}

// WithEmbedPublisher enables post-complete publish of OpportunityEmbedV1
// onto the durable embed queue.
func (p *PostgresProcessor) WithEmbedPublisher(pub EmbedPublisher) *PostgresProcessor {
	p.embeds = pub
	return p
}

// Run is the Frame background consumer loop. Returns nil on clean
// shutdown (ctx cancelled); non-nil errors stop the whole service.
// Never return transient drain errors — log and keep polling.
func (p *PostgresProcessor) Run(ctx context.Context) error {
	log := util.Log(ctx)
	log.WithField("poll", p.poll.String()).WithField("batch", p.batch).
		WithField("concurrency", p.concurrency).
		Info("postgres worker: background drain started")

	ticker := time.NewTicker(p.poll)
	defer ticker.Stop()

	// Immediate first drain so we don't wait a full poll interval after boot.
	if err := p.drain(ctx); err != nil {
		log.WithError(err).Warn("postgres worker: drain failed")
	}

	for {
		select {
		case <-ctx.Done():
			log.Info("postgres worker: background drain stopped")
			return nil
		case <-ticker.C:
			if err := p.drain(ctx); err != nil {
				log.WithError(err).Warn("postgres worker: drain failed")
			}
		}
	}
}

func (p *PostgresProcessor) drain(ctx context.Context) error {
	items, err := p.store.Claim(ctx, p.owner, p.batch, p.lease)
	if err != nil || len(items) == 0 {
		return err
	}

	// Prefer Frame workerpool (managed ants pool + service lifecycle).
	if p.svc != nil && p.svc.WorkManager() != nil {
		if pool, poolErr := p.svc.WorkManager().GetPool(); poolErr == nil && pool != nil {
			return p.drainWithPool(ctx, pool, items)
		}
	}
	// Fallback: sequential (tests without a full Frame service).
	for _, item := range items {
		if processErr := p.process(ctx, item); processErr != nil {
			if retryErr := p.store.Retry(ctx, item, processErr, p.maxAttempts); retryErr != nil {
				util.Log(ctx).WithError(retryErr).WithField("ingest_id", item.ID).
					Error("postgres worker: retry persistence failed")
			}
		}
	}
	return nil
}

// poolSubmitter is the Frame WorkerPool surface we need (testable).
type poolSubmitter interface {
	Submit(ctx context.Context, task func()) error
}

func (p *PostgresProcessor) drainWithPool(ctx context.Context, pool poolSubmitter, items []jobqueue.Item) error {
	var wg sync.WaitGroup
	for _, item := range items {
		item := item
		wg.Add(1)
		submitErr := pool.Submit(ctx, func() {
			defer wg.Done()
			if processErr := p.process(ctx, item); processErr != nil {
				if retryErr := p.store.Retry(ctx, item, processErr, p.maxAttempts); retryErr != nil {
					util.Log(ctx).WithError(retryErr).WithField("ingest_id", item.ID).
						Error("postgres worker: retry persistence failed")
				}
			}
		})
		if submitErr != nil {
			// Pool saturated / closed — process synchronously so the claim
			// is not abandoned until lease expiry.
			wg.Done()
			if processErr := p.process(ctx, item); processErr != nil {
				if retryErr := p.store.Retry(ctx, item, processErr, p.maxAttempts); retryErr != nil {
					util.Log(ctx).WithError(retryErr).WithField("ingest_id", item.ID).
						Error("postgres worker: retry persistence failed")
				}
			}
		}
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
	// Prefer envelope HowToApply; fall back only for legacy payloads that
	// may have stashed it under attributes (then strip so it never persists
	// in the public attributes JSON).
	howToApply := strings.TrimSpace(in.HowToApply)
	if howToApply == "" {
		howToApply = attrString(a, "how_to_apply")
	}
	delete(a, "how_to_apply")
	c := jobqueue.Canonical{
		CandidateID: xid.New().String(), HardKey: in.HardKey, Kind: defaultString(in.Kind, "job"),
		SourceID: in.SourceID, ExternalID: in.ExternalID, Title: in.Title,
		Description: attrString(a, "description"), HowToApply: howToApply, IssuingEntity: in.IssuingEntity,
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
	// External embedding HTTP → Frame Queue (not inline, not Events).
	if p.embeds != nil && canonicalID != "" {
		job := eventsv1.OpportunityEmbedV1{
			OpportunityID: canonicalID,
			Title:         c.Title,
			IssuingEntity: c.IssuingEntity,
			Description:   c.Description,
			Kind:          c.Kind,
			Country:       c.Country,
		}
		if c.AmountMax > 0 {
			v := c.AmountMax
			job.AmountMax = &v
		}
		if c.PostedAt != nil {
			job.PostedAt = c.PostedAt.UTC().Format(time.RFC3339)
		}
		if pubErr := p.embeds.PublishEmbed(ctx, job); pubErr != nil {
			// Ingest already committed; log and rely on embed-backfill /
			// redelivery if the queue accept failed hard.
			util.Log(ctx).WithError(pubErr).WithField("canonical_id", canonicalID).
				Warn("postgres worker: publish embed job failed")
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
