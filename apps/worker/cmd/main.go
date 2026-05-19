// apps/worker/cmd — entrypoint for the job-pipeline worker.
package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	framejskv "github.com/pitabwire/frame/cache/jetstreamkv"
	cachevalkey "github.com/pitabwire/frame/cache/valkey"
	"github.com/pitabwire/frame/data"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"

	workercfg "github.com/stawi-opportunities/opportunities/apps/worker/config"
	workersvc "github.com/stawi-opportunities/opportunities/apps/worker/service"
)

func main() {
	ctx := context.Background()

	cfg, err := workercfg.Load()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: load config")
	}

	// Build a JetStream-KV-backed raw cache and register it with
	// Frame under a single name. Two typed views on it — one for
	// dedup (hard_key → cluster_id) and one for cluster snapshots —
	// are taken below via GetCache with different keyFuncs. The same
	// RawCache is also the backing store for the kv-rebuild path
	// (replaces the prior direct go-redis client; see kv_rebuild.go
	// for the GET+conditional-SET pattern that takes the place of
	// the previous Lua-CAS script).
	// Pick the cache backend. Prefer Valkey when VALKEY_DSN is set —
	// in-cluster Valkey replies in <1ms per Get/Set, vs the 5s
	// request-reply timeout we hit consistently on JetStream-KV in
	// this environment. JetStream-KV stays available as a fallback
	// so the worker can still boot in dev/test envs without Valkey.
	var raw cache.RawCache
	switch {
	case cfg.ValkeyDSN != "":
		raw, err = cachevalkey.New(
			cache.WithDSN(data.DSN(cfg.ValkeyDSN)),
			cache.WithName(cfg.CacheBucket),
		)
		if err != nil {
			util.Log(ctx).WithError(err).WithField("dsn", cfg.ValkeyDSN).Fatal("worker: valkey cache open")
		}
		util.Log(ctx).WithField("dsn", cfg.ValkeyDSN).Info("worker: cache backend = valkey")
	case cfg.CacheNATSURL != "":
		raw, err = framejskv.New(
			cache.WithDSN(data.DSN(cfg.CacheNATSURL)),
			cache.WithCredsFile(cfg.CacheNATSCredsFile),
			cache.WithName(cfg.CacheBucket),
		)
		if err != nil {
			util.Log(ctx).WithError(err).Fatal("worker: jetstream-kv cache open")
		}
		util.Log(ctx).WithField("url", cfg.CacheNATSURL).Info("worker: cache backend = jetstream-kv")
	default:
		util.Log(ctx).Fatal("worker: no cache backend configured (set VALKEY_DSN or CACHE_NATS_URL)")
	}

	// Service wiring. WithDatastore adds the Postgres connection that
	// the pipeline_variants ledger (pkg/variantstate) writes to. The
	// DSN comes from DATABASE_URL the same way the crawler's already
	// consumes it.
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
		frame.WithCacheManager(),
		frame.WithCache("worker", raw),
	)
	defer svc.Stop(ctx)

	// pipeline_variants store — the master ledger that lets ops answer
	// "where is variant X" and that replaces NATS purge as a recovery
	// strategy. Soft-fail on Postgres outages: writes degrade
	// observability but never stall the chain.
	var variantStore *variantstate.Store
	if pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName); pool != nil {
		variantStore = variantstate.NewStore(pool.DB)
		util.Log(ctx).Info("worker: pipeline_variants store wired")
	} else {
		util.Log(ctx).Warn("worker: no datastore pool — pipeline_variants writes disabled")
	}

	// Load the opportunity-kinds registry at boot. Phase 1 only loads + logs;
	// later phases consult the registry on the publish/index paths.
	reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("opportunity registry: load failed")
	}
	util.Log(ctx).WithField("kinds", reg.Known()).Info("opportunity registry: loaded")

	// Initialize pipeline + Iceberg telemetry instruments. The Record*
	// helpers used by embed/translate paths are nil-safe (they drop the
	// sample if telemetry was never initialised) so a failure here does
	// not break the pipeline — but without Init no metrics flow at all.
	if err := telemetry.Init(); err != nil {
		util.Log(ctx).WithError(err).Warn("telemetry metrics init failed")
	}

	// Extractor (AI). nil if no inference URL configured — handlers
	// degrade gracefully.
	var ex *extraction.Extractor
	if cfg.InferenceBaseURL != "" || cfg.EmbeddingBaseURL != "" {
		ex = extraction.New(extraction.Config{
			BaseURL:          cfg.InferenceBaseURL,
			APIKey:           cfg.InferenceAPIKey,
			Model:            cfg.InferenceModel,
			EmbeddingBaseURL: cfg.EmbeddingBaseURL,
			EmbeddingAPIKey:  cfg.EmbeddingAPIKey,
			EmbeddingModel:   cfg.EmbeddingModel,
			Registry:         reg,
			HTTPClient:       svc.HTTPClientManager().Client(ctx),
		})
	}

	// Typed cache views — both back onto the same "worker" raw cache,
	// separated by key-prefix functions.
	// NATS KV keys must match `^[-/_=\.a-zA-Z0-9]+$` — colons are
	// rejected with "nats: invalid key". Use a dash separator instead
	// so dedup + cluster namespacing keeps working against the same
	// JetStream KV bucket without splitting it into two physical
	// buckets. The colon-prefixed form silently failed every Get/Set
	// pre-v8.0.30 (worker errored "nats: invalid key" → handler
	// returned non-nil → NATS redelivered every variant ad infinitum,
	// the canonical chain stayed stuck no matter how fast llama ran).
	dedupCache, ok := cache.GetCache[string, string](
		svc.CacheManager(), "worker",
		func(k string) string { return "dedup-" + k },
	)
	if !ok {
		util.Log(ctx).Fatal("worker: dedup cache wiring failed (GetCache returned nil)")
	}
	clusterCache, ok := cache.GetCache[string, kv.ClusterSnapshot](
		svc.CacheManager(), "worker",
		func(k string) string { return "cluster-" + k },
	)
	if !ok {
		util.Log(ctx).Fatal("worker: cluster cache wiring failed (GetCache returned nil)")
	}

	// R2 publisher for canonical JSON snapshots. Single Cloudflare R2
	// account token + content-bucket name. The worker has no Pages
	// deploy hook — pass empty string.
	publisher := publish.NewR2Publisher(
		cfg.R2AccountID,
		cfg.R2AccessKeyID,
		cfg.R2SecretAccessKey,
		cfg.R2ContentBucket,
		"", // no Pages deploy hook for the worker
	)

	service := workersvc.NewService(svc, ex, publisher, reg, dedupCache, clusterCache, variantStore, cfg.TranslationLangs, cfg.ValidationSkipLLM, cfg.DedupSkipCache, cfg.DedupReadBackend)

	// S3-compatible client for the R2 content bucket (used by kv/rebuild
	// to list <kind>/*.json slug files). Same R2 account credentials —
	// only the bucket name differs across workloads.
	contentBucketEndpoint := cfg.R2Endpoint
	if contentBucketEndpoint == "" {
		contentBucketEndpoint = fmt.Sprintf(
			"https://%s.r2.cloudflarestorage.com",
			cfg.R2AccountID,
		)
	}
	r2S3Client := s3.New(s3.Options{
		Region: "auto",
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.R2AccessKeyID,
			cfg.R2SecretAccessKey,
			"",
		),
		BaseEndpoint: aws.String(contentBucketEndpoint),
	})
	kvRebuilder := workersvc.NewKVRebuilder(r2S3Client, cfg.R2ContentBucket, raw, reg)

	adminMux := http.NewServeMux()
	adminMux.HandleFunc("POST /_admin/kv/rebuild", workersvc.KVRebuildHandler(kvRebuilder))

	// Register the worker pipeline:
	//   - Frame Events for fast in-process stages (normalize, validate,
	//     dedup, canonical, publish).
	//   - Frame Queue for external-LLM stages (embed, translate). Each
	//     gets its own subject + durable consumer so per-stage
	//     backpressure is independent.
	svc.Init(ctx,
		frame.WithRegisterEvents(service.EventHandlers()...),
		frame.WithRegisterPublisher(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL),
		frame.WithRegisterPublisher(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL),
		frame.WithRegisterSubscriber(eventsv1.SubjectWorkerEmbed, cfg.WorkerEmbedQueueURL, service.EmbedWorker()),
		frame.WithRegisterSubscriber(eventsv1.SubjectWorkerTranslate, cfg.WorkerTranslateQueueURL, service.TranslateWorker()),
		frame.WithHTTPHandler(adminMux),
	)

	// The worker consumes svc.opportunities.events.> (catch-all) but
	// only acts on the variants/canonical chain. Loose mode lets Frame
	// ack-and-skip every other topic on the shared stream — replaces
	// the per-topic NoopHandler block that used to live in
	// service.EventHandlers().
	if mgr := svc.EventsManager(); mgr != nil {
		mgr.SetStrict(false)
	}

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: frame.Run failed")
	}
}
