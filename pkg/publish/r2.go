package publish

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Publisher uploads content to a Cloudflare R2 bucket via the S3-compatible API.
type R2Publisher struct {
	client        *s3.Client
	bucket        string
	deployHookURL string
}

// NewR2Publisher creates an R2Publisher configured for the given Cloudflare account.
func NewR2Publisher(accountID, accessKeyID, secretKey, bucket, deployHookURL string) *R2Publisher {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)

	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, ""),
		BaseEndpoint: aws.String(endpoint),
	})

	return &R2Publisher{
		client:        client,
		bucket:        bucket,
		deployHookURL: deployHookURL,
	}
}

// Upload writes content to the given key in the R2 bucket.
func (p *R2Publisher) Upload(ctx context.Context, key string, content []byte) error {
	_, err := p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("text/markdown; charset=utf-8"),
	})
	return err
}

// UploadJSON writes JSON content to the given key.
func (p *R2Publisher) UploadJSON(ctx context.Context, key string, content []byte) error {
	_, err := p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/json; charset=utf-8"),
	})
	return err
}

// UploadPublicSnapshot writes a public JSON snapshot with Cache-Control headers
// suited to CDN edge caching. Browser keeps 1 min, edge keeps 5 min; updates
// propagate naturally within that window without requiring purges.
func (p *R2Publisher) UploadPublicSnapshot(ctx context.Context, key string, content []byte) error {
	_, err := p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(p.bucket),
		Key:          aws.String(key),
		Body:         bytes.NewReader(content),
		ContentType:  aws.String("application/json; charset=utf-8"),
		CacheControl: aws.String("public, max-age=60, s-maxage=300"),
	})
	return err
}

// ObjectKey returns the R2 object key for an opportunity body file.
// The prefix is Spec.URLPrefix (jobs, scholarships, tenders, ...).
func ObjectKey(prefix, slug string) string {
	return prefix + "/" + slug + ".json"
}

// TranslationKey returns the R2 object key for a translated body file.
func TranslationKey(prefix, slug, lang string) string {
	return prefix + "/" + slug + "/" + lang + ".json"
}

// ContentOrigin is the public CDN origin for R2-served content. Overridden at
// deploy time via CONTENT_ORIGIN env var (use SetContentOrigin).
var ContentOrigin = "https://opportunities-data.stawi.org"

// SetContentOrigin overrides the default ContentOrigin. Empty inputs are
// ignored so callers can unconditionally pass env vars.
func SetContentOrigin(origin string) {
	if origin != "" {
		ContentOrigin = origin
	}
}

// PublicURL returns the fully qualified URL of an R2 key on the public origin.
func PublicURL(key string) string {
	return ContentOrigin + "/" + key
}

// Delete removes a key from the R2 bucket.
func (p *R2Publisher) Delete(ctx context.Context, key string) error {
	_, err := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	})
	return err
}

// Download reads content from R2.
func (p *R2Publisher) Download(ctx context.Context, key string) ([]byte, error) {
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Body.Close() }()
	return io.ReadAll(out.Body)
}

// TriggerDeploy POSTs to the Cloudflare Pages deploy hook to trigger a site rebuild.
func (p *R2Publisher) TriggerDeploy() error {
	if p.deployHookURL == "" {
		return nil
	}
	resp, err := http.Post(p.deployHookURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("deploy hook POST failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("deploy hook returned status %d", resp.StatusCode)
	}
	log.Printf("publish: deploy hook triggered successfully (status %d)", resp.StatusCode)
	return nil
}
