// Command worker drains PostgreSQL job ingestion work into the canonical
// PostgreSQL serving tables, then embeds opportunities via Frame Queue.
//
// Long-running process model (Frame async decision tree):
//
//   - Postgres drain loop → frame.WithBackgroundConsumer
//     Frame owns the goroutine and ties exit to service shutdown.
//   - Concurrent claim processing → Frame workerpool (WorkManager ants pool).
//   - Opportunity embedding (external HTTP) → Frame Queue subscriber
//     (WORKER_EMBED_QUEUE_URL) with durable retry — not inline, not Events.
package main

import (
	"context"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/util"

	workercfg "github.com/stawi-opportunities/opportunities/apps/worker/config"
	workersvc "github.com/stawi-opportunities/opportunities/apps/worker/service"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

func main() {
	ctx := context.Background()
	cfg, err := workercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: load config")
	}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		util.Log(ctx).Fatal("worker: DATABASE_URL is required")
	}
	store := jobqueue.New(pool.DB)
	processor := workersvc.NewPostgresProcessor(store, "",
		cfg.PostgresBatchSize, cfg.PostgresConcurrency, cfg.PostgresPollInterval,
		cfg.PostgresLease, cfg.PostgresMaxAttempts,
	).WithService(svc)

	// BackgroundConsumer is registered at Init so Frame starts it inside
	// Run() under managed lifecycle (never a bare go processor.Run).
	initOpts := []frame.Option{
		frame.WithBackgroundConsumer(processor.Run),
	}

	if cfg.EmbeddingBaseURL != "" && cfg.WorkerEmbedQueueURL != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
		)
		ex := extraction.New(extraction.Config{
			EmbeddingBaseURL:    embBase,
			EmbeddingAPIKey:     embKey,
			EmbeddingModel:      embModel,
			EmbeddingDimensions: cfg.EmbeddingDimensions,
			HTTPClient:          svc.HTTPClientManager().Client(ctx),
		})
		embedH := workersvc.NewEmbedHandler(store, ex)
		processor.WithEmbedPublisher(workersvc.NewFrameEmbedPublisher(svc, eventsv1.SubjectWorkerEmbed))
		initOpts = append(initOpts,
			frame.WithRegisterPublisher(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL),
			frame.WithRegisterSubscriber(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL, embedH),
		)
		util.Log(ctx).WithField("model", embModel).WithField("dims", cfg.EmbeddingDimensions).
			WithField("queue", eventsv1.SubjectWorkerEmbed).
			Info("worker: opportunity embedding via Frame Queue")
	} else {
		util.Log(ctx).Warn("worker: embedding disabled (need EMBEDDING_BASE_URL + WORKER_EMBED_QUEUE_URL)")
	}

	svc.Init(ctx, initOpts...)
	// Catch-all events stream: ack-and-skip unknown topics (loose mode).
	if mgr := svc.EventsManager(); mgr != nil {
		mgr.SetStrict(false)
	}

	util.Log(ctx).Info("worker: starting (BackgroundConsumer=postgres drain)")
	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: frame.Run failed")
	}
}
