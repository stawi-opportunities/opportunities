// apps/frontier-worker/cmd — entrypoint for the URL frontier
// fetch loop.
//
// The frontier-worker consumes wake-up events on
// crawl.url.enqueued.v1, dequeues URLs from url_frontier under
// per-host politeness, fetches each URL, archives the raw HTML
// to R2, runs the AI extractor, and emits VariantIngestedV1 into
// the existing pipeline.
//
// Horizontally scalable: multiple replicas race on Dequeue's
// SKIP LOCKED claim and never double-claim. Initial replicas = 2.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/util"

	frontiercfg "github.com/stawi-opportunities/opportunities/apps/frontier-worker/config"
	frontiersvc "github.com/stawi-opportunities/opportunities/apps/frontier-worker/service"
	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/geocode"
	"github.com/stawi-opportunities/opportunities/pkg/normalize"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

func main() {
	ctx := context.Background()

	cfg, err := frontiercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("frontier-worker: load config")
	}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	log := util.Log(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Fatal("frontier-worker: DATABASE_URL required")
	}
	dbFn := pool.DB

	if err := telemetry.Init(); err != nil {
		log.WithError(err).Warn("telemetry metrics init failed")
	}

	// Load opportunity-kinds registry. Prefer R2 → disk fallback,
	// matching apps/crawler.
	loader, err := definitions.NewR2LoaderFromEnv(ctx)
	if err != nil {
		log.WithError(err).Fatal("definitions: env config failed")
	}
	var reg *opportunity.Registry
	if loader != nil {
		if err := loader.Start(ctx); err != nil {
			log.WithError(err).Fatal("definitions: loader start failed")
		}
		reg, err = opportunity.LoadFromDefinitions(ctx, loader)
		if err != nil {
			log.WithError(err).Fatal("definitions: registry load failed")
		}
		log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded from R2 definitions")
	} else {
		log.Warn("definitions: R2 not configured; falling back to OpportunityKindsDir")
		reg, err = opportunity.LoadFromDir(cfg.OpportunityKindsDir)
		if err != nil {
			log.WithError(err).Fatal("opportunity registry: load failed")
		}
		log.WithField("kinds", reg.Known()).Info("opportunity registry: loaded from disk")
	}

	// Repositories.
	sourceRepo := repository.NewSourceRepository(dbFn)
	crawlRepo := repository.NewCrawlRepository(dbFn)
	variantStore := variantstate.NewStore(dbFn)

	// Frontier with the OnEnqueue hook left unset — the
	// frontier-worker doesn't re-enqueue; that's the crawler's
	// job. Emission is wired on the producer side.
	pf := frontier.NewPostgresFrontier(dbFn)

	// Fetcher — plain stdlib client; the per-URL fetch path is
	// short-lived and doesn't need Frame's OAuth wrapping (target
	// hosts are public job boards).
	httpDoer := &http.Client{
		Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second,
	}
	httpClient := httpx.NewClientFromDoer(httpDoer, cfg.UserAgent)

	// AI extractor — same wiring as apps/crawler.
	var extractor *extraction.Extractor
	infBase, infModel, infKey := extraction.ResolveInference(
		cfg.InferenceBaseURL, cfg.InferenceModel, cfg.InferenceAPIKey,
		"", "",
	)
	if infBase != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
			"", "",
		)
		// Dedicated inference client with a generous timeout. The fetch
		// client (httpDoer) is intentionally short (HTTP_TIMEOUT_SEC=20s)
		// for pulling job pages, but the shared llama fleet queues
		// requests internally well beyond its parallel slots — a 20s
		// deadline cancels LLM calls that would have answered in 60-180s
		// (observed: "context deadline exceeded ... awaiting headers").
		// Mirrors apps/crawler's HTTP_CLIENT_TIMEOUT=5m. Reusing httpDoer
		// here was the bug.
		inferenceDoer := &http.Client{Timeout: 5 * time.Minute}
		extractor = extraction.New(extraction.Config{
			BaseURL:          infBase,
			APIKey:           infKey,
			Model:            infModel,
			EmbeddingBaseURL: embBase,
			EmbeddingAPIKey:  embKey,
			EmbeddingModel:   embModel,
			Registry:         reg,
			HTTPClient:       inferenceDoer,
		})
		log.WithField("url", infBase).WithField("model", infModel).Info("AI extraction enabled")
	}

	// Geocoder + normalizer mirror apps/crawler.
	geocoder := geocode.New()
	normalizer := normalize.New(geocoder)

	// Archive — same R2 archive bucket as the crawler so reparse
	// + retention paths see one consistent set of raw_payloads.
	arch := archive.NewR2Archive(archive.R2Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2ArchiveBucket,
	})

	handler := frontiersvc.NewHandler(frontiersvc.Deps{
		Svc:           svc,
		IngestedQueue: cfg.QueuePipelineIngestedName,
		Frontier:      pf,
		Sources:      sourceRepo,
		Kinds:        reg,
		Archive:      arch,
		Extractor:    extractor,
		Normalizer:   normalizer,
		Fetcher:      httpClient,
		VariantStore: variantStore,
		CrawlRepo:    crawlRepo,
		DequeueBatch: cfg.DequeueBatch,
		MaxAttempts:  cfg.MaxAttempts,
		IdleTick:     time.Duration(cfg.IdleTickSeconds) * time.Second,
	})

	// Definitions broadcast — same live-reload pattern as the
	// rest of the apps. Plug in alongside the URL-enqueued
	// subscriber.
	handlers := []events.EventI{handler}
	if loader != nil {
		rebuild := func(ctx context.Context) error {
			fresh, err := opportunity.LoadFromDefinitions(ctx, loader)
			if err != nil {
				return err
			}
			reg.Replace(fresh)
			return nil
		}
		handlers = append(handlers, definitions.NewBroadcastConsumer(loader, rebuild))
		log.WithField("topic", eventsv1.TopicDefinitionsChanged).Info("definitions: broadcast consumer wired")
	}

	svc.Init(ctx,
		frame.WithRegisterEvents(handlers...),
		frame.WithRegisterPublisher(cfg.QueuePipelineIngestedName, cfg.QueuePipelineIngested),
	)

	// Loose mode — the worker subscribes to the catch-all
	// svc.opportunities.events.> but only acts on URL-enqueued.
	// Frame ack-and-skips everything else without dead-lettering.
	if mgr := svc.EventsManager(); mgr != nil {
		mgr.SetStrict(false)
	}

	// Heartbeat ticker — fires Dequeue every IdleTick even when
	// no NATS wake-up arrives. Ensures progress under JetStream
	// rebalances and during initial backlog drain.
	log.WithField("worker_id", handler.WorkerID()).
		WithField("idle_tick", cfg.IdleTickSeconds).
		Info("frontier-worker: heartbeat ticker starting")
	go func() {
		t := time.NewTicker(time.Duration(cfg.IdleTickSeconds) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				handler.Tick(ctx)
			}
		}
	}()

	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Fatal("frontier-worker: frame.Run failed")
	}
}
