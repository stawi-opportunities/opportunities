//go:build integration

package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"

	writersvc "stawi.jobs/apps/writer/service"
)

func TestWriterE2EVariantIngested(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 1. MinIO
	mc, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-03T04-33-23Z")
	if err != nil {
		t.Fatalf("minio.Run: %v", err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })

	endpoint, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	cfg := eventlog.R2Config{
		AccountID:       "test",
		AccessKeyID:     mc.Username,
		SecretAccessKey: mc.Password,
		Bucket:          "stawi-jobs-log-e2e",
		Endpoint:        "http://" + endpoint,
		UsePathStyle:    true,
	}
	client := eventlog.NewClient(cfg)
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	uploader := eventlog.NewUploader(client, cfg.Bucket)

	// 2. Frame service — use in-memory queue (default mem:// URL) and noop HTTP driver.
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("writer-e2e"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	// 3. Buffer with short MaxInterval so the flusher triggers fast.
	buf := writersvc.NewBuffer(writersvc.Thresholds{
		MaxEvents:   100,
		MaxBytes:    64 << 20,
		MaxInterval: 250 * time.Millisecond,
	})
	wService := writersvc.NewService(svc, buf, uploader, 250*time.Millisecond)
	if err := wService.RegisterSubscriptions([]string{eventsv1.TopicVariantsIngested}); err != nil {
		t.Fatalf("RegisterSubscriptions: %v", err)
	}

	// Start the flusher goroutine.
	go func() { _ = wService.RunFlusher(ctx) }()

	// svc.Run initialises the queue (publisher + subscriber) and blocks
	// until ctx is done. Run it in a goroutine; the deferred svc.Stop /
	// ctx cancel above will end it cleanly at test exit.
	runErrc := make(chan error, 1)
	go func() { runErrc <- svc.Run(ctx, "") }()

	// Give the queue subscriber time to initialise before emitting.
	time.Sleep(200 * time.Millisecond)

	// 4. Publish an event. The WriterHandler.PayloadType returns *json.RawMessage
	// so Frame's Unmarshal will copy the bytes directly. We emit the
	// JSON-encoded Envelope as json.RawMessage so Marshal passes it through
	// without double-encoding.
	now := time.Now().UTC()
	env := eventsv1.NewEnvelope(
		eventsv1.TopicVariantsIngested,
		eventsv1.VariantIngestedV1{
			VariantID: "var_e2e_1",
			SourceID:  "src_e2e",
			HardKey:   "src_e2e|e2e1",
			Stage:     "ingested",
			Title:     "E2E Test Job",
			ScrapedAt: now,
		},
	)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := svc.EventsManager().Emit(ctx, eventsv1.TopicVariantsIngested, json.RawMessage(raw)); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// 5. Wait for the event to be consumed, buffered, flushed, and uploaded.
	expectedPrefix := "variants/dt=" + now.Format("2006-01-02") + "/src=src_e2e/"
	waitUntil := time.Now().Add(15 * time.Second)
	for time.Now().Before(waitUntil) {
		list, lerr := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(cfg.Bucket),
			Prefix: aws.String(expectedPrefix),
		})
		if lerr == nil && list.KeyCount != nil && *list.KeyCount > 0 {
			first := *list.Contents[0].Key
			if !strings.HasPrefix(first, expectedPrefix) {
				t.Fatalf("unexpected key %q, want prefix %q", first, expectedPrefix)
			}
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for flushed file under %q", expectedPrefix)
}
