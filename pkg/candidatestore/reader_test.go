package candidatestore

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

func TestReaderReturnsLatestEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })
	endpoint, _ := mc.ConnectionString(ctx)

	cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-cand",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	client := eventlog.NewClient(cfg)
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	uploader := eventlog.NewUploader(client, cfg.Bucket)

	// Seed two embedding events for cnd_abc (prefix "ab"), one older,
	// one newer. The reader must return the newer one.
	older := eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_abc", CVVersion: 1, Vector: []float32{0.1, 0.2}, ModelVersion: "v1"}
	newer := eventsv1.CandidateEmbeddingV1{CandidateID: "cnd_abc", CVVersion: 2, Vector: []float32{0.9, 0.8}, ModelVersion: "v1"}

	for i, pl := range []eventsv1.CandidateEmbeddingV1{older, newer} {
		body, werr := eventlog.WriteParquet([]eventsv1.CandidateEmbeddingV1{pl})
		if werr != nil {
			t.Fatalf("WriteParquet: %v", werr)
		}
		key := "candidates_embeddings_current/cnd=ab/" + []string{"v1", "v2"}[i] + ".parquet"
		if _, err := uploader.Put(ctx, key, body); err != nil {
			t.Fatalf("upload: %v", err)
		}
	}

	r := NewReader(client, cfg.Bucket)
	vec, err := r.LatestEmbedding(ctx, "cnd_abc")
	if err != nil {
		t.Fatalf("LatestEmbedding: %v", err)
	}
	if len(vec.Vector) != 2 || vec.Vector[0] != 0.9 || vec.CVVersion != 2 {
		t.Fatalf("expected v2 vector, got %+v", vec)
	}
}

func TestReaderReturnsErrNotFoundWhenNoEmbedding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })
	endpoint, _ := mc.ConnectionString(ctx)

	cfg := eventlog.R2Config{
		AccountID: "test", AccessKeyID: mc.Username, SecretAccessKey: mc.Password,
		Bucket: "stawi-jobs-log-empty", Endpoint: "http://" + endpoint, UsePathStyle: true,
	}
	client := eventlog.NewClient(cfg)
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	r := NewReader(client, cfg.Bucket)
	_, err = r.LatestEmbedding(ctx, "cnd_missing")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
