// apps/frontier-worker/cmd — entrypoint for the URL frontier
// fetch loop.
//
// The frontier-worker consumes wake-up events on
// crawl.url.enqueued.v1, dequeues URLs from url_frontier under
// per-host politeness, fetches and parses each URL in memory, then enqueues the
// extracted result.
//
// Horizontally scalable: multiple replicas race on Dequeue's
// SKIP LOCKED claim and never double-claim. Initial replicas = 2.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/events"
	"github.com/pitabwire/util"

	frontiercfg "github.com/stawi-opportunities/opportunities/apps/frontier-worker/config"
	frontiersvc "github.com/stawi-opportunities/opportunities/apps/frontier-worker/service"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/geocode"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/pkg/normalize"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
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
	// Unblocker fallback — identical wiring to apps/crawler: blocked
	// requests retry through scrape.do / the proxy, direct successes
	// never pay for it.
	var doer httpx.HTTPDoer = httpDoer
	unblocker, desc, insecure, uerr := httpx.NewUnblocker(httpx.UnblockerConfig{
		ScrapeDoToken:   cfg.ScrapeDoToken,
		ScrapeDoRender:  cfg.ScrapeDoRender,
		ScrapeDoSuper:   cfg.ScrapeDoSuper,
		ScrapeDoGeoCode: cfg.ScrapeDoGeoCode,
		ProxyURL:        cfg.UnblockerProxyURL,
		ProxyCACert:     cfg.UnblockerCACert,
		Timeout:         time.Duration(cfg.UnblockerTimeoutSec) * time.Second,
	}, httpDoer)
	switch {
	case uerr != nil:
		log.WithError(uerr).Warn("unblocker fallback disabled: invalid configuration")
	case unblocker != nil:
		doer = httpx.NewFallbackDoer(httpDoer, unblocker)
		if insecure {
			log.WithField("via", desc).Warn("unblocker fallback enabled WITHOUT a pinned CA — proxy TLS is not verified")
		} else {
			log.WithField("via", desc).Info("unblocker fallback enabled for blocked requests")
		}
	}
	httpClient := httpx.NewClientFromDoer(doer, cfg.UserAgent)

	// Geocoder + normalizer (no LLM on this path).
	geocoder := geocode.New()
	normalizer := normalize.New(geocoder)

	handler := frontiersvc.NewHandler(frontiersvc.Deps{
		Svc:                svc,
		IngestQueue:        jobqueue.NewProducer(dbFn, cfg.IngestMaxPending),
		IngestMaxPending:   cfg.IngestMaxPending,
		IngestMaxOldestAge: cfg.IngestMaxOldestAge,
		Frontier:           pf,
		Sources:            sourceRepo,
		Kinds:              reg,
		Normalizer:         normalizer,
		Fetcher:            httpClient,
		DequeueBatch:       cfg.DequeueBatch,
		MaxAttempts:        cfg.MaxAttempts,
		IdleTick:           time.Duration(cfg.IdleTickSeconds) * time.Second,
	})
	log.Info("frontier-worker: schema.org JobPosting extract only")

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

	// Heartbeat ticker — fires Dequeue every IdleTick even when
	// no NATS wake-up arrives. Frame BackgroundConsumer owns the
	// goroutine (not a bare go) and cancels with service shutdown.
	idleTick := time.Duration(cfg.IdleTickSeconds) * time.Second
	if idleTick <= 0 {
		idleTick = 30 * time.Second
	}
	log.WithField("worker_id", handler.WorkerID()).
		WithField("idle_tick", idleTick).
		Info("frontier-worker: heartbeat via Frame BackgroundConsumer")

	svc.Init(ctx,
		frame.WithRegisterEvents(handlers...),
		frame.WithBackgroundConsumer(func(bgCtx context.Context) error {
			t := time.NewTicker(idleTick)
			defer t.Stop()
			for {
				select {
				case <-bgCtx.Done():
					return nil
				case <-t.C:
					handler.Tick(bgCtx)
				}
			}
		}),
	)

	// Loose mode — the worker subscribes to the catch-all
	// svc.opportunities.events.> but only acts on URL-enqueued.
	// Frame ack-and-skips everything else without dead-lettering.
	if mgr := svc.EventsManager(); mgr != nil {
		mgr.SetStrict(false)
	}

	if err := svc.Run(ctx, ""); err != nil {
		log.WithError(err).Fatal("frontier-worker: frame.Run failed")
	}
}
