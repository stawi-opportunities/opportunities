package archive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type R2Archive struct {
	client *s3.Client
	bucket string
}

var _ Archive = (*R2Archive)(nil)

type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

func NewR2Archive(cfg R2Config) *R2Archive {
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		BaseEndpoint: aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)),
	})
	return &R2Archive{client: client, bucket: cfg.Bucket}
}

func (a *R2Archive) PutRaw(ctx context.Context, body []byte) (string, int64, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(body); err != nil {
		return "", 0, fmt.Errorf("gzip raw: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", 0, fmt.Errorf("gzip close: %w", err)
	}
	_, err := a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(a.bucket), Key: aws.String(RawKey(hash)),
		Body: bytes.NewReader(compressed.Bytes()), ContentType: aws.String("application/octet-stream"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return "", 0, fmt.Errorf("put raw: %w", err)
	}
	return hash, int64(len(body)), nil
}

func (a *R2Archive) GetRaw(ctx context.Context, hash string) ([]byte, error) {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(RawKey(hash))})
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
		return nil, fmt.Errorf("read raw: %w", err)
	}
	return body, nil
}

func (a *R2Archive) HasRaw(ctx context.Context, hash string) (bool, error) {
	_, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(RawKey(hash))})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("head raw: %w", err)
}

func isNotFound(err error) bool {
	var noKey *s3types.NoSuchKey
	var notFound *s3types.NotFound
	return errors.As(err, &noKey) || errors.As(err, &notFound) || strings.Contains(err.Error(), "StatusCode: 404")
}
