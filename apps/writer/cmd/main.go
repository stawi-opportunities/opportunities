// apps/writer/cmd — entrypoint for the event-log writer service.
//
// The writer subscribes to every job-pipeline topic, buffers incoming
// events per (partition_dt, partition_secondary), and flushes them to
// Iceberg via Transaction.Append on size/count/time triggers.
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	// Side-effect import: registers s3/s3a/s3n/gs/mem/abfs IO scheme
	// factories with iceberg-go's filesystem registry. Without this,
	// every catalog metadata load against s3://... fails with
	// "io scheme not registered for path …" and the writer can't
	// commit anything (variants, variants_rejected, crawl_page_completed
	// all dead-letter through Frame redelivery). The gocloud blob
	// adapter wraps aws-sdk-go-v2/s3 so the writer's existing R2
	// credentials flow continues to work.
	_ "github.com/apache/iceberg-go/io/gocloud"

	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/icebergclient"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"

	writercfg "github.com/stawi-opportunities/opportunities/apps/writer/config"
	writersvc "github.com/stawi-opportunities/opportunities/apps/writer/service"
)

func main() {
	ctx := context.Background()

	// Subcommand dispatch. The bootstrap-iceberg subcommand is invoked
	// by the opportunities-iceberg-bootstrap Kubernetes Job on every
	// FluxCD reconcile to enable R2 Data Catalog on the chronicle
	// bucket + create every namespace/table. Idempotent.
	if len(os.Args) > 1 && os.Args[1] == "bootstrap-iceberg" {
		if err := runBootstrap(ctx); err != nil {
			util.Log(ctx).WithError(err).Fatal("bootstrap-iceberg failed")
		}
		return
	}

	cfg, err := writercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: load config")
	}

	opts := []frame.Option{
		frame.WithConfig(&cfg),
	}

	ctx, svc := frame.NewServiceWithContext(ctx, opts...)
	defer svc.Stop(ctx)

	// Load the opportunity-kinds registry. Prefer the R2-backed
	// definitions loader (admin can edit kind YAMLs in the cluster);
	// fall back to the on-disk ConfigMap when R2 isn't configured so
	// dev/OSS deploys keep working.
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

	// Initialize pipeline + Iceberg telemetry instruments. Without this,
	// every commit-retry path in the writer hits a nil-Counter and SIGSEGVs
	// (the production-observed cause of the writer crashloop). Frame has
	// already configured the global OTel provider, so this registers our
	// custom instruments into it; failures here are non-fatal — we just
	// log and continue with no metrics rather than block writes on a
	// telemetry-collector outage.
	if err := telemetry.Init(); err != nil {
		util.Log(ctx).WithError(err).Warn("telemetry metrics init failed")
	}

	// Open the Iceberg REST catalog (R2 Data Catalog).
	cat, err := icebergclient.LoadCatalog(ctx, icebergclient.CatalogConfig{
		Name:       cfg.IcebergCatalogName,
		URI:        cfg.IcebergCatalogURI,
		Warehouse:  cfg.IcebergWarehouse,
		OAuthToken: cfg.IcebergCatalogToken,
	})
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: catalog load failed")
	}

	buffer := writersvc.NewBuffer(writersvc.Thresholds{
		MaxEvents:   cfg.FlushMaxEvents,
		MaxBytes:    cfg.FlushMaxBytes,
		MaxInterval: cfg.FlushMaxInterval,
	})

	wService := writersvc.NewService(svc, buffer, cat, cfg.FlushMaxInterval)
	// Events-bus topics (crawl/source observability, variant-rejection, the
	// candidate-side lifecycle) stay on the shared Frame Events stream.
	if err := wService.RegisterSubscriptions(writersvc.EventBusTopics()); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: register subscriptions failed")
	}

	// Pipeline-stage topics now flow on dedicated Frame Queues. The writer is
	// a fan-out durable consumer of each (its own consumer_durable_name on the
	// same subject the in-pipeline consumer drains), funnelling the stage event
	// into the same buffer for Iceberg archival. Name+URI must match the
	// worker's QUEUE_PIPELINE_* env. mem:// is the local/test default.
	pipelineQueues := map[string][2]string{
		eventsv1.TopicVariantsIngested:   {cfg.QueuePipelineIngestedName, cfg.QueuePipelineIngested},
		eventsv1.TopicVariantsNormalized: {cfg.QueuePipelineNormalizedName, cfg.QueuePipelineNormalized},
		eventsv1.TopicVariantsValidated:  {cfg.QueuePipelineValidatedName, cfg.QueuePipelineValidated},
		eventsv1.TopicVariantsFlagged:    {cfg.QueuePipelineFlaggedName, cfg.QueuePipelineFlagged},
		eventsv1.TopicVariantsClustered:  {cfg.QueuePipelineClusteredName, cfg.QueuePipelineClustered},
		eventsv1.TopicEmbeddings:         {cfg.QueuePipelineEmbeddingsName, cfg.QueuePipelineEmbeddings},
		eventsv1.TopicPublished:          {cfg.QueuePipelinePublishedName, cfg.QueuePipelinePublished},
	}
	var queueOpts []frame.Option
	for _, topic := range writersvc.PipelineQueueTopics() {
		nameURI, ok := pipelineQueues[topic]
		if !ok {
			util.Log(ctx).WithField("topic", topic).Fatal("writer: pipeline topic missing queue config")
		}
		queueOpts = append(queueOpts,
			frame.WithRegisterSubscriber(nameURI[0], nameURI[1], wService.QueueWorker(topic)))
	}
	svc.Init(ctx, queueOpts...)

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

	go func() {
		if err := wService.RunFlusher(ctx); err != nil {
			util.Log(ctx).WithError(err).Error("writer: flusher exited")
		}
	}()

	// Register OTel Iceberg observables (catalog-backed gauges).
	// Must be called after the catalog is loaded.
	telemetry.RegisterIcebergObservables(telemetry.IcebergObservablesConfig{
		Catalog:     cat,
		TableIdents: writersvc.AppendOnlyTables,
	})

	// Register writer buffer stats for the buffer gauges.
	telemetry.RegisterBufferStats(func(topic string) (int, int) {
		return buffer.StatsForTopic(topic)
	}, eventsv1.AllTopics())

	// Admin HTTP mux — lightweight, not exposed to the public internet.
	// Trustage fires POST /_admin/expire-snapshots nightly and
	// POST /_admin/compact every 2 h via the in-cluster service DNS
	// (opportunities-writer.opportunities.svc).
	expireCfg := writersvc.ExpireSnapshotsConfig{
		OlderThan:          time.Duration(cfg.SnapshotRetentionDays) * 24 * time.Hour,
		MinSnapshotsToKeep: cfg.MinSnapshotsToKeep,
		PerTableTimeout:    5 * time.Minute,
		Parallelism:        4,
	}
	compactCfg := writersvc.CompactConfig{
		TargetFileSize:    cfg.CompactTargetFileSize,
		MinFileSize:       cfg.CompactMinFileSize,
		MaxInputPerCommit: cfg.CompactMaxInputPerCommit,
		PerTableTimeout:   cfg.CompactPerTableTimeout,
		Parallelism:       cfg.CompactParallelism,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_admin/expire-snapshots",
		writersvc.ExpireSnapshotsHandler(cat, expireCfg))
	mux.HandleFunc("POST /_admin/compact",
		writersvc.CompactHandler(cat, compactCfg))

	svc.Init(ctx, frame.WithHTTPHandler(mux))

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("writer: frame.Run failed")
	}
}
