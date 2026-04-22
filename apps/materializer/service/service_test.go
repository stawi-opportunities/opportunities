//go:build integration

package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/glebarez/sqlite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/searchindex"

	matsvc "stawi.jobs/apps/materializer/service"
)

// startManticoreForMat starts a Manticore testcontainer and returns
// (httpURL, stopFn). Mirrors pkg/searchindex/schema_test.go's helper;
// duplicated here because _test files can't be imported across
// packages and the helper is cheap to repeat.
//
// IMPORTANT: passes Env: {"EXTRA":"1"} so the KNN library is loaded
// — without it, Apply() fails with "knn library not loaded".
// Startup timeout is 120 s because EXTRA=1 triggers a one-time
// library download on first run.
func startManticoreForMat(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "manticoresearch/manticore:6.3.2",
		ExposedPorts: []string{"9308/tcp"},
		Env:          map[string]string{"EXTRA": "1"},
		WaitingFor:   wait.ForListeningPort("9308/tcp").WithStartupTimeout(120 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start manticore: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "9308/tcp")
	url := fmt.Sprintf("http://%s:%s", host, port.Port())
	return url, func() { _ = c.Terminate(context.Background()) }
}

func TestMaterializerE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- MinIO (R2) ---
	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })

	endpoint, _ := mc.ConnectionString(ctx)
	r2cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-mat",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	r2c := eventlog.NewClient(r2cfg)
	if _, err := r2c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(r2cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	// --- Manticore (HTTP JSON API) ---
	url, stopManticore := startManticoreForMat(t, ctx)
	defer stopManticore()
	mtc, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		t.Fatalf("manticore open: %v", err)
	}
	defer func() { _ = mtc.Close() }()
	if err := searchindex.Apply(ctx, mtc); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	// --- SQLite watermarks ---
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	// AutoMigrate uses the struct's gorm tag `default:now()` which is
	// Postgres-only and fails on SQLite. Create the table directly instead.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS materializer_watermarks (
		prefix       TEXT PRIMARY KEY,
		last_r2_key  TEXT NOT NULL DEFAULT '',
		updated_at   DATETIME NOT NULL DEFAULT (datetime('now'))
	)`).Error; err != nil {
		t.Fatalf("migrate: %v", err)
	}
	watermarks := repository.NewWatermarkRepository(func(_ context.Context, _ bool) *gorm.DB { return db })

	// --- Seed one canonical Parquet file ---
	now := time.Now().UTC()
	can := eventsv1.CanonicalUpsertedV1{
		CanonicalID:  "can_e2e_1",
		ClusterID:    "clu_1",
		Slug:         "e2e-senior-engineer-acme",
		Title:        "E2E Senior Engineer",
		Company:      "Acme",
		Description:  "We are hiring an engineer",
		Country:      "KE",
		RemoteType:   "remote",
		Category:     "programming",
		Status:       "active",
		QualityScore: 75,
		PostedAt:     now,
		LastSeenAt:   now,
		ExpiresAt:    now.Add(120 * 24 * time.Hour),
	}
	body, err := eventlog.WriteParquet([]eventsv1.CanonicalUpsertedV1{can})
	if err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	pk := eventsv1.PartitionKey(eventsv1.TopicCanonicalsUpserted, now, can.ClusterID)
	objKey := pk.ObjectPath("canonicals", "e2e-mat")
	up := eventlog.NewUploader(r2c, r2cfg.Bucket)
	if _, err := up.Put(ctx, objKey, body); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// --- Run one poll tick ---
	reader := eventlog.NewReader(r2c, r2cfg.Bucket)
	indexer := matsvc.NewIndexer(mtc)
	service := matsvc.NewService(reader, indexer, watermarks, []string{"canonicals/"}, 100*time.Millisecond, 50)

	pollCtx, cancelPoll := context.WithTimeout(ctx, 15*time.Second)
	done := make(chan struct{})
	go func() { _ = service.Run(pollCtx); close(done) }()

	// Wait until Manticore has the row. Use /search HTTP to exercise
	// the same path the API will use.
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		raw, qerr := mtc.Search(ctx, map[string]any{
			"index": "idx_jobs_rt",
			"query": map[string]any{
				"equals": map[string]any{"country": "KE"},
			},
			"limit": 10,
		})
		if qerr == nil && strings.Contains(string(raw), `"can_e2e_1"`) {
			cancelPoll()
			<-done
			return // success
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancelPoll()
	<-done
	t.Fatal("materializer did not propagate canonical into Manticore within deadline")
}
