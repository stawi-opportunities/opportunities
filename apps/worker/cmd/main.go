// Command worker drains PostgreSQL job ingestion work into the canonical
// PostgreSQL serving tables, then embeds opportunities via Frame Queue.
//
// Long-running process model (Frame async decision tree):
//
//   - Postgres drain loop → frame.WithBackgroundConsumer
//     Frame owns the goroutine and ties exit to service shutdown.
//   - Concurrent claim processing → Frame workerpool (WorkManager ants pool).
//   - Opportunity embedding (external HTTP) → Frame Queue subscriber
//     (WORKER_EMBED_QUEUE_URL) with durable retry — prefer GCP Pub/Sub.
//   - Path A fan-out → MATCHING_FANOUT_QUEUE_URL (GCP Pub/Sub to matching).
//
// Critical data path is Postgres (crawl queue + product catalog). Pub/Sub
// carries async stages that can absorb bursts. NATS is not required here.
package main

import (
	"context"
	"os"
	"strings"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/util"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	// GCP Pub/Sub for embed + fan-out (durable, multi-consumer capable).
	_ "gocloud.dev/pubsub/gcppubsub"

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
	if cfg.ProductDatabaseURL != "" {
		productGorm, err := gorm.Open(postgres.Open(cfg.ProductDatabaseURL), &gorm.Config{})
		if err != nil {
			util.Log(ctx).WithError(err).Fatal("worker: PRODUCT_DATABASE_URL open failed")
		}
		store = store.WithProductDB(func(context.Context, bool) *gorm.DB { return productGorm })
		util.Log(ctx).Info("worker: dual-DB mode (crawl DATABASE_URL + product PRODUCT_DATABASE_URL)")
	} else {
		util.Log(ctx).Warn("worker: PRODUCT_DATABASE_URL unset — single-DB mode (dev only)")
	}
	processor := workersvc.NewPostgresProcessor(store, "",
		cfg.PostgresBatchSize, cfg.PostgresConcurrency, cfg.PostgresPollInterval,
		cfg.PostgresLease, cfg.PostgresMaxAttempts,
	).WithService(svc)

	// BackgroundConsumer is registered at Init so Frame starts it inside
	// Run() under managed lifecycle (never a bare go processor.Run).
	initOpts := []frame.Option{
		frame.WithBackgroundConsumer(processor.Run),
	}

	// Optional durable embed + fan-out via Pub/Sub. Never register a GCP
	// transport without credentials — Frame fatals on open failure and would
	// take down the postgres drain. Skip quietly when misconfigured.
	embedURL := strings.TrimSpace(cfg.WorkerEmbedQueueURL)
	embedSubURL := strings.TrimSpace(cfg.WorkerEmbedSubscribeURL)
	if embedSubURL == "" {
		embedSubURL = embedURL
	}
	fanOutURL := strings.TrimSpace(cfg.MatchingFanOutQueueURL)

	canGCP := gcpCredentialsOK()
	if (isGCPURL(embedURL) || isGCPURL(fanOutURL)) && !canGCP {
		util.Log(ctx).Warn("worker: GCP Pub/Sub URLs set but GOOGLE_APPLICATION_CREDENTIALS missing/unreadable — embed/fan-out disabled; drain continues")
		embedURL, embedSubURL, fanOutURL = "", "", ""
	}

	if cfg.EmbeddingBaseURL != "" && embedURL != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
		)
		ex := extraction.New(extraction.Config{
			EmbeddingBaseURL:    embBase,
			EmbeddingAPIKey:     embKey,
			EmbeddingModel:      embModel,
			EmbeddingDimensions: cfg.EmbeddingDimensions,
			EmbeddingInputType:  cfg.EmbeddingInputType,
			HTTPClient:          svc.HTTPClientManager().Client(ctx),
		})
		embedH := workersvc.NewEmbedHandler(store, ex)
		processor.WithEmbedPublisher(workersvc.NewFrameEmbedPublisher(svc, eventsv1.SubjectWorkerEmbed))
		initOpts = append(initOpts,
			frame.WithRegisterPublisher(eventsv1.SubjectWorkerEmbed, embedURL),
			frame.WithRegisterSubscriber(eventsv1.SubjectWorkerEmbed, embedSubURL, embedH),
		)
		if fanOutURL != "" {
			// Path A: after embed, publish to matching so new jobs fan out live.
			embedH.WithFanOutPublisher(workersvc.NewFrameFanOutPublisher(svc, eventsv1.SubjectOpportunityFanOut))
			initOpts = append(initOpts,
				frame.WithRegisterPublisher(eventsv1.SubjectOpportunityFanOut, fanOutURL),
			)
			util.Log(ctx).WithField("queue", eventsv1.SubjectOpportunityFanOut).
				Info("worker: opportunity fan-out publish enabled")
		} else {
			util.Log(ctx).Warn("worker: MATCHING_FANOUT_QUEUE_URL unset — Path A fan-out not published")
		}
		util.Log(ctx).WithField("model", embModel).WithField("dims", cfg.EmbeddingDimensions).
			WithField("publish", embedURL).WithField("subscribe", embedSubURL).
			Info("worker: opportunity embedding via Frame Queue")
	} else {
		util.Log(ctx).Warn("worker: embedding queue disabled (need EMBEDDING_BASE_URL + WORKER_EMBED_QUEUE_URL + credentials)")
	}

	svc.Init(ctx, initOpts...)
	// Catch-all events stream: ack-and-skip unknown topics (loose mode).
	// Worker does not depend on NATS Events for the ingest path.
	if mgr := svc.EventsManager(); mgr != nil {
		mgr.SetStrict(false)
	}

	util.Log(ctx).Info("worker: starting (BackgroundConsumer=postgres drain)")
	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: frame.Run failed")
	}
}

func isGCPURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(u, "gcppubsub://") || strings.Contains(u, "protocol=gcppubsub")
}

func gcpCredentialsOK() bool {
	// ADC via well-known env, or GCE/GKE metadata (not available on OCI).
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return true
		}
		return false
	}
	// Explicit opt-in for environments that inject ADC another way.
	return os.Getenv("GOOGLE_CLOUD_ALLOW_DEFAULT_ADC") == "true"
}
