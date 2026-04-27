//go:build integration

package eventlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/eventlog"
)

func TestParquetRoundTripViaMinio(t *testing.T) {
	ctx := context.Background()

	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(ctx) })

	endpoint, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	cfg := eventlog.R2Config{
		AccountID:       "test-account",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "opportunities-log-test",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	client := eventlog.NewClient(cfg)

	// Ensure bucket exists — testcontainer-minio boots with none.
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	rows := []eventsv1.VariantIngestedV1{
		{
			VariantID:     "var_1",
			SourceID:      "src_greenhouse",
			ExternalID:    "abc123",
			HardKey:       "src_greenhouse|abc123",
			Kind:          "job",
			Stage:         "ingested",
			Title:         "Senior Backend Engineer",
			IssuingEntity: "Acme",
			AnchorCountry: "KE",
			ScrapedAt:     time.Now().UTC(),
		},
	}

	buf, err := eventlog.WriteParquet(rows)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	if len(buf) == 0 {
		t.Fatal("WriteParquet produced empty buffer")
	}

	pk := eventsv1.PartitionKey(eventsv1.TopicVariantsIngested, rows[0].ScrapedAt, rows[0].SourceID)
	objKey := pk.ObjectPath("variants", "test-xid")

	up := eventlog.NewUploader(client, cfg.Bucket)
	etag, err := up.Put(ctx, objKey, buf)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if etag == "" {
		t.Fatal("Put returned empty ETag")
	}

	// Verify we can read the object back and decode it.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(objKey),
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer func() { _ = got.Body.Close() }()

	var roundTrip []byte
	tmp := make([]byte, 8*1024)
	for {
		n, err := got.Body.Read(tmp)
		if n > 0 {
			roundTrip = append(roundTrip, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}

	decoded, err := eventlog.ReadParquet[eventsv1.VariantIngestedV1](roundTrip)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 row, got %d", len(decoded))
	}
	if decoded[0].VariantID != "var_1" || decoded[0].SourceID != "src_greenhouse" {
		t.Fatalf("row lost on round-trip: %+v", decoded[0])
	}
}
