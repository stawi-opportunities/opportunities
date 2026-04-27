// Package eventlog writes and uploads the durable event log to R2.
// It is shared by apps/writer (Phase 1), the compactor admin endpoint
// (Phase 2), and eventual backfill jobs (Phase 6).
package eventlog

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Config carries the bucket + credentials for the *event log*
// bucket. This is a different bucket from pkg/archive: the archive
// holds raw HTML; the event log holds Parquet. Separate buckets let
// operators apply different lifecycle policies and access grants.
type R2Config struct {
	// AccountID is the Cloudflare account identifier. The S3 endpoint
	// is derived as https://{AccountID}.r2.cloudflarestorage.com.
	AccountID string

	// AccessKeyID / SecretAccessKey are R2 API tokens with write
	// access scoped to the event-log bucket only (least privilege).
	AccessKeyID     string
	SecretAccessKey string

	// Bucket is the event-log bucket name (e.g. cluster-chronicle).
	Bucket string

	// Endpoint allows tests to point at a MinIO container. Empty
	// falls back to the CF R2 endpoint derived from AccountID.
	Endpoint string

	// UsePathStyle is required for MinIO. R2 itself tolerates both
	// but path-style is safer for test fixtures.
	UsePathStyle bool
}

// NewClient constructs an S3-compatible client for the event log bucket.
func NewClient(cfg R2Config) *s3.Client {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}
	return s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: cfg.UsePathStyle,
	})
}

// Uploader uploads already-serialized Parquet buffers to R2.
// Kept separate from Writer so tests can unit-test writing without
// R2 and unit-test uploading without Parquet.
type Uploader struct {
	client *s3.Client
	bucket string
}

// NewUploader wraps an S3 client + bucket for Put.
func NewUploader(client *s3.Client, bucket string) *Uploader {
	return &Uploader{client: client, bucket: bucket}
}

// Put writes body at objectKey and returns the ETag on success.
// ContentType is set to application/vnd.apache.parquet for operators
// poking around the bucket; R2 / S3 do not interpret it.
func (u *Uploader) Put(ctx context.Context, objectKey string, body []byte) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("eventlog: refusing to upload empty body at %q", objectKey)
	}
	resp, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(u.bucket),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/vnd.apache.parquet"),
	})
	if err != nil {
		return "", fmt.Errorf("eventlog: put %q: %w", objectKey, err)
	}
	if resp.ETag == nil {
		return "", nil
	}
	return *resp.ETag, nil
}
