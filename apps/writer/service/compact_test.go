package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

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

// TestCompactHourly_DedupAndMerge seeds three small Parquet files with
// overlapping event_ids and asserts dedup/merge. Earliest occurred_at wins.
func TestCompactHourly_DedupAndMerge(t *testing.T) {
	ctx := context.Background()
	mch := startMinIO(t)
	defer mch.Close()

	uploader := eventlog.NewUploader(mch.Client, mch.Bucket)
	reader := eventlog.NewReader(mch.Client, mch.Bucket)

	hour := time.Date(2026, 4, 22, 13, 0, 0, 0, time.UTC)
	dt := hour.Format("2006-01-02")

	rowsA := []eventsv1.VariantIngestedV1{
		{EventID: "v1", VariantID: "var-1", SourceID: "acme", OccurredAt: hour.Add(2 * time.Minute)},
		{EventID: "v2", VariantID: "var-2", SourceID: "acme", OccurredAt: hour.Add(3 * time.Minute)},
	}
	rowsB := []eventsv1.VariantIngestedV1{
		{EventID: "v2", VariantID: "var-2", SourceID: "acme", OccurredAt: hour.Add(1 * time.Minute)},
		{EventID: "v3", VariantID: "var-3", SourceID: "acme", OccurredAt: hour.Add(4 * time.Minute)},
	}
	rowsC := []eventsv1.VariantIngestedV1{
		{EventID: "v4", VariantID: "var-4", SourceID: "acme", OccurredAt: hour.Add(5 * time.Minute)},
	}
	for i, rows := range [][]eventsv1.VariantIngestedV1{rowsA, rowsB, rowsC} {
		body, err := eventlog.WriteParquet(rows)
		require.NoError(t, err)
		key := "variants/dt=" + dt + "/src=acme/part-" + string(rune('a'+i)) + ".parquet"
		_, err = uploader.Put(ctx, key, body)
		require.NoError(t, err)
	}

	c := NewCompactor(mch.Client, reader, uploader, mch.Bucket)

	got, err := c.CompactHourly(ctx, CompactHourlyInput{
		Collection: "variants",
		Hour:       hour,
	})
	require.NoError(t, err)
	require.Equal(t, 4, got.RowsAfter, "deduped: v1+v2+v3+v4")
	require.Equal(t, 5, got.RowsBefore)
	require.Equal(t, 1, got.FilesAfter)
	require.Equal(t, 3, got.FilesDeleted)

	// Merged file should contain the earliest v2 occurred_at.
	list, err := mch.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(mch.Bucket),
		Prefix: aws.String("variants/dt=" + dt + "/src=acme/"),
	})
	require.NoError(t, err)
	require.Len(t, list.Contents, 1)

	body := getObjHelper(t, mch.Client, mch.Bucket, *list.Contents[0].Key)
	merged, err := eventlog.ReadParquet[eventsv1.VariantIngestedV1](body)
	require.NoError(t, err)
	var v2 eventsv1.VariantIngestedV1
	for _, r := range merged {
		if r.EventID == "v2" {
			v2 = r
		}
	}
	require.Equal(t, hour.Add(1*time.Minute).UTC(), v2.OccurredAt.UTC())
}

func getObjHelper(t *testing.T, cli *s3.Client, bucket, key string) []byte {
	t.Helper()
	out, err := cli.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	defer func() { _ = out.Body.Close() }()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(out.Body)
	require.NoError(t, err)
	return buf.Bytes()
}
