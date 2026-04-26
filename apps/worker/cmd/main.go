// apps/worker/cmd — entrypoint for the job-pipeline worker.
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	framevalkey "github.com/pitabwire/frame/cache/valkey"
	"github.com/pitabwire/frame/data"
	"github.com/pitabwire/util"
	"github.com/redis/go-redis/v9"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"

	workercfg "github.com/stawi-opportunities/opportunities/apps/worker/config"
	workersvc "github.com/stawi-opportunities/opportunities/apps/worker/service"
)

func main() {
	ctx := context.Background()

	cfg, err := workercfg.Load()
	if err != nil {
		log.Fatalf("worker: load config: %v", err)
	}

	// Build a Valkey-backed raw cache and register it with Frame
	// under a single name. Two typed views on it — one for dedup
	// (hard_key → cluster_id) and one for cluster snapshots — are
	// taken below via GetCache with different keyFuncs.
	raw, err := framevalkey.New(cache.WithDSN(data.DSN(cfg.ValkeyURL)))
	if err != nil {
		log.Fatalf("worker: valkey cache open: %v", err)
	}

	// Direct go-redis client on the same Valkey instance, used by the
	// KV rebuild admin endpoint which writes raw JSON rather than going
	// through Frame's typed cache layer.
	kvOpts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		log.Fatalf("worker: parse valkey URL: %v", err)
	}
	kvClient := redis.NewClient(kvOpts)

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithCacheManager(),
		frame.WithCache("worker", raw),
	)
	defer svc.Stop(ctx)

	// Load the opportunity-kinds registry at boot. Phase 1 only loads + logs;
	// later phases consult the registry on the publish/index paths.
	reg, err := opportunity.LoadFromDir(cfg.OpportunityKindsDir)
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("opportunity registry: load failed")
	}
	util.Log(ctx).WithField("kinds", reg.Known()).Info("opportunity registry: loaded")

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
		})
	}

	// Typed cache views — both back onto the same "worker" raw cache,
	// separated by key-prefix functions.
	dedupCache, ok := cache.GetCache[string, string](
		svc.CacheManager(), "worker",
		func(k string) string { return "dedup:" + k },
	)
	if !ok {
		util.Log(ctx).Fatal("worker: dedup cache wiring failed (GetCache returned nil)")
	}
	clusterCache, ok := cache.GetCache[string, kv.ClusterSnapshot](
		svc.CacheManager(), "worker",
		func(k string) string { return "cluster:" + k },
	)
	if !ok {
		util.Log(ctx).Fatal("worker: cluster cache wiring failed (GetCache returned nil)")
	}

	// R2 publisher for canonical JSON snapshots.
	// pkg/publish.NewR2Publisher takes positional args:
	// accountID, accessKeyID, secretKey, bucket, deployHookURL.
	// The worker has no Pages deploy hook — pass empty string.
	publisher := publish.NewR2Publisher(
		cfg.R2PublishAccountID,
		cfg.R2PublishAccessKeyID,
		cfg.R2PublishSecretAccessKey,
		cfg.R2PublishBucket,
		"", // no Pages deploy hook for the worker
	)

	service := workersvc.NewService(svc, ex, publisher, reg, dedupCache, clusterCache, cfg.TranslationLangs)

	// S3-compatible client for the R2 content bucket (used by kv/rebuild
	// to list jobs/*.json slug files). Reuses the publish bucket credentials.
	contentBucketEndpoint := fmt.Sprintf(
		"https://%s.r2.cloudflarestorage.com",
		cfg.R2PublishAccountID,
	)
	r2S3Client := s3.New(s3.Options{
		Region: "auto",
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.R2PublishAccessKeyID,
			cfg.R2PublishSecretAccessKey,
			"",
		),
		BaseEndpoint: aws.String(contentBucketEndpoint),
	})
	kvRebuilder := workersvc.NewKVRebuilder(r2S3Client, cfg.R2ContentBucket, kvClient, reg)

	adminMux := http.NewServeMux()
	adminMux.HandleFunc("POST /_admin/kv/rebuild", workersvc.KVRebuildHandler(kvRebuilder))

	svc.Init(ctx,
		frame.WithRegisterEvents(service.Handlers()...),
		frame.WithHTTPHandler(adminMux),
	)

	if err := svc.Run(ctx, ""); err != nil {
		util.Log(ctx).WithError(err).Fatal("worker: frame.Run failed")
	}
}
