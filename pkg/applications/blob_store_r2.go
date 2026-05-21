// pkg/applications/blob_store_r2.go
package applications

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// R2BlobStore is the production BlobStore backed by Cloudflare R2 over
// the S3-compatible API. Used by the applications service for
// attachment uploads (CV PDFs, screenshots, etc.).
type R2BlobStore struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// R2BlobConfig carries the credentials + bucket for an R2 endpoint.
type R2BlobConfig struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

// NewR2BlobStore returns a configured R2 backend. Errors when any of
// the required config fields are empty.
func NewR2BlobStore(cfg R2BlobConfig) (*R2BlobStore, error) {
	if cfg.AccountID == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" || cfg.Bucket == "" {
		return nil, errors.New("applications: R2BlobConfig requires AccountID, AccessKeyID, SecretAccessKey, Bucket")
	}
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
	})
	return &R2BlobStore{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  cfg.Bucket,
	}, nil
}

// Compile-time check that R2BlobStore satisfies the BlobStore interface.
var _ BlobStore = (*R2BlobStore)(nil)

// Put uploads body under key with the given content-type and returns the
// number of bytes written. The body is buffered in memory before upload
// because the S3 SDK requires a seekable reader or a known content-length.
// Attachments are capped at 25 MiB by the handler so this is bounded.
func (b *R2BlobStore) Put(ctx context.Context, key, contentType string, body io.Reader) (int64, error) {
	buf, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("applications: r2 read: %w", err)
	}
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(buf),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return 0, fmt.Errorf("applications: r2 put: %w", err)
	}
	return int64(len(buf)), nil
}

// Get returns the blob body. The caller must close the returned reader.
// Returns ErrBlobNotFound when no object exists at key.
func (b *R2BlobStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nfe *s3types.NoSuchKey
		if errors.As(err, &nfe) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("applications: r2 get: %w", err)
	}
	return out.Body, nil
}

// Delete removes the blob at key. Deleting a non-existent key is a no-op.
func (b *R2BlobStore) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("applications: r2 delete: %w", err)
	}
	return nil
}

// PresignGet returns a pre-signed download URL valid for ttl. Default ttl is
// 15 minutes (per spec §5.4) when ttl <= 0 is passed.
func (b *R2BlobStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	req, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("applications: r2 presign: %w", err)
	}
	return req.URL, nil
}
