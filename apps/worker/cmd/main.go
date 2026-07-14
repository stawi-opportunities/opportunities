// Command worker drains PostgreSQL job ingestion work into the canonical
// PostgreSQL serving tables.
package main

import (
	"context"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/util"

	workercfg "github.com/stawi-opportunities/opportunities/apps/worker/config"
	workersvc "github.com/stawi-opportunities/opportunities/apps/worker/service"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

func main() {
	ctx := context.Background()
	cfg, err := workercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: load config")
	}

	ctx, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&cfg), frame.WithDatastore())
	defer svc.Stop(ctx)
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		util.Log(ctx).Fatal("worker: DATABASE_URL is required")
	}

	processor := workersvc.NewPostgresProcessor(jobqueue.New(pool.DB), "",
		cfg.PostgresBatchSize, cfg.PostgresConcurrency, cfg.PostgresPollInterval,
		cfg.PostgresLease, cfg.PostgresMaxAttempts)
	if cfg.EmbeddingBaseURL != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
		)
		ex := extraction.New(extraction.Config{
			EmbeddingBaseURL:    embBase,
			EmbeddingAPIKey:     embKey,
			EmbeddingModel:      embModel,
			EmbeddingDimensions: cfg.EmbeddingDimensions,
		})
		processor.WithEmbedder(ex)
		util.Log(ctx).WithField("model", embModel).WithField("dims", cfg.EmbeddingDimensions).
			Info("worker: opportunity embedding enabled")
	} else {
		util.Log(ctx).Warn("worker: EMBEDDING_BASE_URL empty — opportunities stored without vectors")
	}
	go processor.Run(ctx)
	util.Log(ctx).Info("worker: PostgreSQL ingestion processor started")

	svc.Init(ctx)
	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: frame.Run failed")
	}
}
