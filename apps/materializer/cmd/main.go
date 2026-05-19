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

	reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("opportunity registry: load failed")
	}
	util.Log(ctx).WithField("kinds", reg.Known()).Info("opportunity registry: loaded")

	telemetry.RegisterIcebergObservables(telemetry.IcebergObservablesConfig{
		TableIdents: nil,
	})

	service := matsvc.NewService(svc, store)
	if err := service.RegisterSubscriptions(); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: register subscriptions failed")
	}

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("materializer: frame.Run failed")
	}
}
