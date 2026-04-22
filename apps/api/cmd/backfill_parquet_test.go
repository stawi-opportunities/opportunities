//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

type fakeR2Snapshotter struct {
	uploads map[string][]byte
}

func (f *fakeR2Snapshotter) UploadPublicSnapshot(_ context.Context, key string, body []byte) error {
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
	}
	f.uploads[key] = body
	return nil
}
func (f *fakeR2Snapshotter) TriggerDeploy() error { return nil }

type minioCmdHarness struct {
	Client *s3.Client
	Bucket string
	mc     *minio.MinioContainer
}

func (h *minioCmdHarness) Close() { _ = h.mc.Terminate(context.Background()) }

func startMinIOCmd(t *testing.T) *minioCmdHarness {
	t.Helper()
	ctx := context.Background()
	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	require.NoError(t, err)
	endpoint, err := mc.ConnectionString(ctx)
	require.NoError(t, err)
	cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-backfill-test",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	cli := eventlog.NewClient(cfg)
	_, err = cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)})
	require.NoError(t, err)
	return &minioCmdHarness{Client: cli, Bucket: cfg.Bucket, mc: mc}
}

func TestBackfillParquet_PublishesAboveThreshold(t *testing.T) {
	ctx := context.Background()
	mch := startMinIOCmd(t)
	defer mch.Close()

	uploader := eventlog.NewUploader(mch.Client, mch.Bucket)
	now := time.Now().UTC()
	rows := []eventsv1.CanonicalUpsertedV1{
		{CanonicalID: "c1", ClusterID: "cl1", Slug: "acme-eng", Title: "Eng",
			Status: "active", QualityScore: 75, PostedAt: now, OccurredAt: now},
		{CanonicalID: "c2", ClusterID: "cl2", Slug: "low-qual", Title: "Low",
			Status: "active", QualityScore: 30, PostedAt: now, OccurredAt: now},
	}
	body, _ := eventlog.WriteParquet(rows)
	_, err := uploader.Put(ctx, "canonicals_current/cc=cl/current.parquet", body)
	require.NoError(t, err)

	snap := &fakeR2Snapshotter{}
	h := backfillParquetHandler(eventlog.NewReader(mch.Client, mch.Bucket), snap, 50.0)

	req := httptest.NewRequest("POST", "/admin/backfill?min_quality=50", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, 200, rr.Code, rr.Body.String())
	require.Contains(t, snap.uploads, "jobs/acme-eng.json")
	require.NotContains(t, snap.uploads, "jobs/low-qual.json", "below min_quality threshold")

	var snapJSON map[string]any
	require.NoError(t, json.Unmarshal(snap.uploads["jobs/acme-eng.json"], &snapJSON))
	require.Equal(t, "acme-eng", snapJSON["slug"])

	require.Contains(t, rr.Body.String(), `"done":true`)
	_ = bytes.NewReader(nil)
}
