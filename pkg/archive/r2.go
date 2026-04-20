// pkg/archive/r2.go
package archive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// R2Archive is the production Archive backed by Cloudflare R2.
// It talks to R2 via the S3-compatible API. Bucket and credentials
// are private and distinct from the public job-repo bucket.
type R2Archive struct {
	client *s3.Client
	bucket string
}

// Compile-time check that R2Archive satisfies the Archive interface.
// Catches signature drift the moment either side changes.
var _ Archive = (*R2Archive)(nil)

// R2Config carries the four pieces of config needed to talk to
// the archive bucket. The archive bucket MUST be separate from
// the public job-repo bucket — least-privilege credentials
// matter for a store that holds full raw HTML.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

func NewR2Archive(cfg R2Config) *R2Archive {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
	})
	return &R2Archive{client: client, bucket: cfg.Bucket}
}

func (a *R2Archive) PutRaw(ctx context.Context, body []byte) (string, int64, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		return "", 0, fmt.Errorf("gzip raw: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", 0, fmt.Errorf("gzip close: %w", err)
	}

	_, err := a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(a.bucket),
		Key:             aws.String(RawKey(hash)),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentType:     aws.String("text/html; charset=utf-8"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return "", 0, fmt.Errorf("put raw: %w", err)
	}
	return hash, int64(len(body)), nil
}

func (a *R2Archive) GetRaw(ctx context.Context, hash string) ([]byte, error) {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(RawKey(hash)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get raw: %w", err)
	}
	defer func() { _ = out.Body.Close() }()

	gz, err := gzip.NewReader(out.Body)
	if err != nil {
		return nil, fmt.Errorf("gunzip raw: %w", err)
	}
	defer func() { _ = gz.Close() }()

	body, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("read raw %s: %w", hash, err)
	}
	return body, nil
}

func (a *R2Archive) HasRaw(ctx context.Context, hash string) (bool, error) {
	_, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(RawKey(hash)),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("head raw: %w", err)
	}
	return true, nil
}

func (a *R2Archive) PutCanonical(ctx context.Context, clusterID string, snap CanonicalSnapshot) error {
	return a.putJSON(ctx, CanonicalKey(clusterID), snap)
}

func (a *R2Archive) GetCanonical(ctx context.Context, clusterID string) (CanonicalSnapshot, error) {
	var snap CanonicalSnapshot
	err := a.getJSON(ctx, CanonicalKey(clusterID), &snap)
	return snap, err
}

func (a *R2Archive) PutVariant(ctx context.Context, clusterID, variantID string, v VariantBlob) error {
	return a.putJSON(ctx, VariantKey(clusterID, variantID), v)
}

func (a *R2Archive) GetVariant(ctx context.Context, clusterID, variantID string) (VariantBlob, error) {
	var v VariantBlob
	err := a.getJSON(ctx, VariantKey(clusterID, variantID), &v)
	return v, err
}

func (a *R2Archive) PutManifest(ctx context.Context, clusterID string, m Manifest) error {
	return a.putJSON(ctx, ManifestKey(clusterID), m)
}

func (a *R2Archive) GetManifest(ctx context.Context, clusterID string) (Manifest, error) {
	var m Manifest
	err := a.getJSON(ctx, ManifestKey(clusterID), &m)
	return m, err
}

func (a *R2Archive) DeleteCluster(ctx context.Context, clusterID string) error {
	prefix := ClusterDir(clusterID)
	var continuation *string
	for {
		list, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(a.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuation,
		})
		if err != nil {
			return fmt.Errorf("list cluster objects: %w", err)
		}
		if len(list.Contents) == 0 {
			return nil
		}
		ids := make([]s3types.ObjectIdentifier, 0, len(list.Contents))
		for _, obj := range list.Contents {
			ids = append(ids, s3types.ObjectIdentifier{Key: obj.Key})
		}
		out, err := a.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(a.bucket),
			Delete: &s3types.Delete{Objects: ids},
		})
		if err != nil {
			return fmt.Errorf("delete cluster objects: %w", err)
		}
		if len(out.Errors) > 0 {
			// S3 returns 200 OK even when a subset of keys fails — the
			// failures are reported in the Errors slice. Surface the first
			// one; the purge sweeper will retry on the next pass and we'd
			// rather leave a cluster marked "not purged" than silently
			// leak orphans in the archive bucket.
			first := out.Errors[0]
			return fmt.Errorf("delete cluster objects: %d failures, first: key=%s code=%s message=%s",
				len(out.Errors),
				aws.ToString(first.Key),
				aws.ToString(first.Code),
				aws.ToString(first.Message))
		}
		if list.IsTruncated == nil || !*list.IsTruncated {
			return nil
		}
		continuation = list.NextContinuationToken
	}
}

func (a *R2Archive) DeleteRaw(ctx context.Context, hash string) error {
	_, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(RawKey(hash)),
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete raw: %w", err)
	}
	return nil
}

func (a *R2Archive) putJSON(ctx context.Context, key string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	_, err = a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json; charset=utf-8"),
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func (a *R2Archive) getJSON(ctx context.Context, key string, dst any) error {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("get %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return fmt.Errorf("read %s: %w", key, err)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("unmarshal %s: %w", key, err)
	}
	return nil
}

// Client returns the underlying S3 client. Exposed for ops tasks
// (e.g. reconciliation) that need prefix listing beyond the narrow
// Archive interface. Not for use in request paths.
func (a *R2Archive) Client() *s3.Client { return a.client }

// Bucket returns the configured bucket name. Same scope as Client().
func (a *R2Archive) Bucket() string { return a.bucket }

// newR2ArchiveWithEndpoint is the test-only constructor that points
// the client at an arbitrary S3-compatible endpoint (minio in CI).
// Production paths use NewR2Archive.
//
// Unlike NewR2Archive it uses path-style addressing — minio rejects
// virtual-host-style addressing for bucket operations by default.
func newR2ArchiveWithEndpoint(endpoint, accessKey, secretKey, bucket string) *R2Archive {
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
	})
	return &R2Archive{client: client, bucket: bucket}
}

// isNotFound normalises the S3 not-found error. R2 returns a mix
// of NoSuchKey and 404 depending on operation.
func isNotFound(err error) bool {
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	// HeadObject sometimes returns a raw http 404 wrapped in an
	// APIError with code "NotFound" but no typed sentinel.
	return strings.Contains(err.Error(), "StatusCode: 404")
}
