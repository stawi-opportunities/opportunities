//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/wait"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// minioHarness holds a running MinIO container and a pre-configured S3 client.
type minioHarness struct {
	Client *s3.Client
	Bucket string
	mc     *minio.MinioContainer
}

func (h *minioHarness) Close() { _ = h.mc.Terminate(context.Background()) }

func startMinIO(t *testing.T) *minioHarness {
	t.Helper()
	ctx := context.Background()
	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	endpoint, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}
	cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-test",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	cli := eventlog.NewClient(cfg)
	if _, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return &minioHarness{Client: cli, Bucket: cfg.Bucket, mc: mc}
}

// valkeyHarness holds a running Valkey container and a go-redis client.
type valkeyHarness struct {
	Client    *redis.Client
	container testcontainers.Container
}

func (h *valkeyHarness) Close() { _ = h.container.Terminate(context.Background()) }

func startValkey(t *testing.T) *valkeyHarness {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "valkey/valkey:7.2",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "6379")
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: host + ":" + port.Port()})
	require.NoError(t, client.Ping(ctx).Err())
	return &valkeyHarness{Client: client, container: c}
}

func TestKVRebuild_Run(t *testing.T) {
	ctx := context.Background()
	mch := startMinIO(t)
	defer mch.Close()
	kv := startValkey(t)
	defer kv.Close()

	uploader := eventlog.NewUploader(mch.Client, mch.Bucket)
	reader := eventlog.NewReader(mch.Client, mch.Bucket)

	now := time.Now().UTC()
	rows := []eventsv1.CanonicalUpsertedV1{
		{
			EventID:     "e1",
			ClusterID:   "c1",
			CanonicalID: "can-1",
			Title:       "Backend Engineer",
			Company:     "Acme",
			Country:     "KE",
			OccurredAt:  now,
		},
		{
			EventID:     "e2",
			ClusterID:   "c2",
			CanonicalID: "can-2",
			Title:       "Frontend Engineer",
			Company:     "Globex",
			Country:     "NG",
			OccurredAt:  now,
		},
	}

	body, err := eventlog.WriteParquet(rows)
	require.NoError(t, err)
	_, err = uploader.Put(ctx, "canonicals_current/cc=c1/current.parquet", body)
	require.NoError(t, err)

	r := NewKVRebuilder(reader, kv.Client)
	res, err := r.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, res.Rows)
	require.Equal(t, 1, res.Files)
	require.Equal(t, 2, res.ClusterKeysSet)

	// Verify cluster:c1 was written with a meaningful snapshot.
	val, err := kv.Client.Get(ctx, "cluster:c1").Result()
	require.NoError(t, err)
	require.NotEmpty(t, val)
	require.Contains(t, val, "can-1")

	// Verify cluster:c2 was written.
	val, err = kv.Client.Get(ctx, "cluster:c2").Result()
	require.NoError(t, err)
	require.NotEmpty(t, val)
	require.Contains(t, val, "can-2")
}

// TestKVRebuild_SkipsEmptyClusterID verifies rows with no cluster_id are not written.
func TestKVRebuild_SkipsEmptyClusterID(t *testing.T) {
	ctx := context.Background()
	mch := startMinIO(t)
	defer mch.Close()
	kv := startValkey(t)
	defer kv.Close()

	uploader := eventlog.NewUploader(mch.Client, mch.Bucket)
	reader := eventlog.NewReader(mch.Client, mch.Bucket)

	rows := []eventsv1.CanonicalUpsertedV1{
		{EventID: "e1", ClusterID: "", CanonicalID: "can-1", OccurredAt: time.Now().UTC()},
		{EventID: "e2", ClusterID: "c2", CanonicalID: "can-2", OccurredAt: time.Now().UTC()},
	}
	body, err := eventlog.WriteParquet(rows)
	require.NoError(t, err)
	_, err = uploader.Put(ctx, "canonicals_current/part-0.parquet", body)
	require.NoError(t, err)

	r := NewKVRebuilder(reader, kv.Client)
	res, err := r.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, res.Rows)
	// Only 1 key set — the row with empty ClusterID is skipped.
	require.Equal(t, 1, res.ClusterKeysSet)
}
