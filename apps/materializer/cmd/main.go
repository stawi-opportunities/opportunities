// apps/materializer/cmd — entrypoint for the opportunities-table
// materializer.
//
// The materializer is now a pure Frame subscriber writing directly to
// the Postgres opportunities table. Manticore was retired during the
// Postgres + pg_search consolidation (see
// deployment.manifests/SHARED_TASK_NOTES.md): worker.canonical writes
// the canonical row in-process, and the materializer handles the
// embedding + admin-action paths (source-stopped, auto-flagged,
// canonical-expired).
//
// Consumer lag is observable via the NATS Prometheus exporter's
// `nats_jetstream_consumer_num_pending` metric — same as before the
// consolidation.
package main

import (
	"context"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"

	matcfg "github.com/stawi-opportunities/opportunities/apps/materializer/config"
	matsvc "github.com/stawi-opportunities/opportunities/apps/materializer/service"
)

func main() {
	ctx := context.Background()

	cfg, err := matcfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: load config")
	}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		util.Log(ctx).Fatal("materializer: DATABASE_URL required for Postgres-only writes")
	}
	store := variantstate.NewStore(pool.DB)
	util.Log(ctx).Info("materializer: opportunities store wired")

	// Fail loudly if the schema's vector dimension and the configured
	// embedding model disagree. Without this guard a mismatch makes
	// pgvector reject every UpdateEmbedding, which soft-fails per row —
	// silently leaving the whole corpus unembedded and search empty.
	if err := store.VerifyEmbeddingDim(ctx, cfg.EmbeddingDim); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: embedding dimension guard failed")
	}
	util.Log(ctx).WithField("embedding_dim", cfg.EmbeddingDim).Info("materializer: embedding dimension verified")

	// Load the opportunity-kinds registry. Prefer the R2-backed
	// definitions loader; fall back to the on-disk ConfigMap when R2
	// isn't configured (materializer's deploy doesn't ship R2 creds
	// today — the disk path is the normal mode here).
	loader, err := definitions.NewR2LoaderFromEnv(ctx)
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("definitions: env config failed")
	}
	var reg *opportunity.Registry
	if loader != nil {
		if err := loader.Start(ctx); err != nil {
			util.Log(ctx).WithError(err).Fatal("definitions: loader start failed")
		}
		reg, err = opportunity.LoadFromDefinitions(ctx, loader)
		if err != nil {
			util.Log(ctx).WithError(err).Fatal("definitions: registry load failed")
		}
		util.Log(ctx).WithField("kinds", reg.Known()).Info("opportunity registry: loaded from R2 definitions")
	} else {
		util.Log(ctx).Warn("definitions: R2 not configured; falling back to OpportunityKindsDir")
		reg, err = opportunity.LoadFromDir(cfg.OpportunityKindsDir)
		if err != nil {
			util.Log(ctx).WithError(err).Fatal("opportunity registry: load failed")
		}
		util.Log(ctx).WithField("kinds", reg.Known()).Info("opportunity registry: loaded from disk")
	}

	telemetry.RegisterIcebergObservables(telemetry.IcebergObservablesConfig{
		TableIdents: nil,
	})

	service := matsvc.NewService(svc, store)
	if err := service.RegisterSubscriptions(); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: register subscriptions failed")
	}

	// Embeddings arrive on a dedicated Frame Queue (service-profile idiom):
	// the worker's embed stage publishes EmbeddingV1 to pipeline_embeddings,
	// and the materializer is its durable consumer (own consumer_durable_name
	// on the same subject). This is the one pipeline output the materializer
	// writes — it lands the vector into opportunities.embedding. The admin-
	// plane handlers (source-stopped/auto-flagged/canonical-expired) stay on
	// the low-volume events bus via RegisterSubscriptions above.
	svc.Init(ctx,
		frame.WithRegisterSubscriber(cfg.QueuePipelineEmbeddingsName, cfg.QueuePipelineEmbeddings, service.EmbeddingWorker()),
	)

	// definitions.changed.v1 broadcast — invalidates the loader cache
	// and live-rebuilds the kind registry on admin edits.
	if loader != nil {
		rebuild := func(ctx context.Context) error {
			fresh, err := opportunity.LoadFromDefinitions(ctx, loader)
			if err != nil {
				return err
			}
			reg.Replace(fresh)
			return nil
		}
		if mgr := svc.EventsManager(); mgr != nil {
			mgr.Add(definitions.NewBroadcastConsumer(loader, rebuild))
			util.Log(ctx).WithField("topic", eventsv1.TopicDefinitionsChanged).Info("definitions: broadcast consumer wired")
		}
	}

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: frame.Run failed")
	}
}
