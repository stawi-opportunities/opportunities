package eventlog

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Reader lists + downloads R2 objects under a given prefix. Kept
// separate from Uploader so tests can stub each side independently.
type Reader struct {
	client *s3.Client
	bucket string
}

// NewReader wraps an S3 client + bucket for list/get.
func NewReader(client *s3.Client, bucket string) *Reader {
	return &Reader{client: client, bucket: bucket}
}

// ListNewObjects returns object keys under `prefix` that sort after
// `startAfter`. Limit caps the batch size so one poll tick can't
// hog the materializer for an unbounded duration on cold starts.
// Results are sorted lexicographically (R2 returns them that way).
func (r *Reader) ListNewObjects(ctx context.Context, prefix, startAfter string, limit int32) ([]s3types.Object, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(r.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(limit),
	}
	if startAfter != "" {
		input.StartAfter = aws.String(startAfter)
	}
	out, err := r.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("eventlog: list %q: %w", prefix, err)
	}
	return out.Contents, nil
}

// Get fetches an object's bytes. Parses nothing — callers use
// ReadParquet on the body.
func (r *Reader) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("eventlog: get %q: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, out.Body); err != nil {
		return nil, fmt.Errorf("eventlog: read %q: %w", key, err)
	}
	return buf.Bytes(), nil
}
