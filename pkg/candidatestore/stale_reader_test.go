package candidatestore

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/stretchr/testify/require"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// minioHandle holds a running MinIO container and a ready S3 client.
type minioHandle struct {
	Client *s3.Client
	Bucket string
	mc     *minio.MinioContainer
}

func (h *minioHandle) Close() {
	_ = h.mc.Terminate(context.Background())
}

// startMinIO launches a MinIO container, creates the test bucket, and
// returns a handle. The test is failed immediately if the container
// cannot start.
func startMinIO(t *testing.T) *minioHandle {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}

	endpoint, _ := mc.ConnectionString(ctx)
	bucket := "stawi-jobs-log-stale-test"

	cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          bucket,
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	client := eventlog.NewClient(cfg)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	if _, err := client.CreateBucket(ctx2, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		_ = mc.Terminate(context.Background())
		t.Fatalf("create bucket: %v", err)
	}

	return &minioHandle{Client: client, Bucket: bucket, mc: mc}
}

func TestStaleReader_ListStale(t *testing.T) {
	ctx := context.Background()
	mch := startMinIO(t)
	defer mch.Close()

	uploader := eventlog.NewUploader(mch.Client, mch.Bucket)

	now := time.Now().UTC()
	cutoff := now.Add(-60 * 24 * time.Hour)

	rows := []eventsv1.CVExtractedV1{
		{CandidateID: "c1aaaa", OccurredAt: now.Add(-7 * 24 * time.Hour)},
		{CandidateID: "c2bbbb", OccurredAt: now.Add(-120 * 24 * time.Hour)},
		{CandidateID: "c3cccc", OccurredAt: now.Add(-5 * time.Minute)},
	}
	for _, r := range rows {
		body, err := eventlog.WriteParquet([]eventsv1.CVExtractedV1{r})
		require.NoError(t, err)
		key := "candidates_cv_current/cnd=" + r.CandidateID[:2] + "/current-" + r.CandidateID + ".parquet"
		_, err = uploader.Put(ctx, key, body)
		require.NoError(t, err)
	}

	sr := NewStaleReader(mch.Client, mch.Bucket)
	stale, err := sr.ListStale(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	require.Equal(t, "c2bbbb", stale[0].CandidateID)
}
