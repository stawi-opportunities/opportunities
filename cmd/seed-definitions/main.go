// seed-definitions uploads the bootstrap definition YAMLs from
// definitions/opportunity-kinds/ to R2 under definitions/kind/.
//
// Idempotent: every PUT overwrites. Re-running is safe and is the
// expected operator workflow when git-side definitions change AND
// the operator wants to wipe out any admin-UI-side edits that drifted.
//
// Reads R2_ACCOUNT_ID / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY /
// R2_CONTENT_BUCKET from the environment — same secret the crawler/api
// already have.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	var (
		source = flag.String("source", "definitions/opportunity-kinds", "Local source directory")
		prefix = flag.String("prefix", "definitions/kind", "R2 key prefix")
	)
	flag.Parse()

	accountID := os.Getenv("R2_ACCOUNT_ID")
	bucket := os.Getenv("R2_CONTENT_BUCKET")
	if accountID == "" || bucket == "" {
		die("R2_ACCOUNT_ID + R2_CONTENT_BUCKET required")
	}

	ctx := context.Background()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("R2_ACCESS_KEY_ID"),
			os.Getenv("R2_SECRET_ACCESS_KEY"),
			"",
		)),
	)
	if err != nil {
		die("aws config: %v", err)
	}
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
	cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	seeded := 0
	err = filepath.WalkDir(*source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		key := *prefix + "/" + name + ".yaml"
		_, err = cli.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(body),
			ContentType: aws.String("application/x-yaml"),
		})
		if err != nil {
			return fmt.Errorf("put %s: %w", key, err)
		}
		sum := sha256.Sum256(body)
		fmt.Printf("seeded %s (sha256=%s)\n", key, hex.EncodeToString(sum[:8]))
		seeded++
		return nil
	})
	if err != nil {
		die("walk: %v", err)
	}
	fmt.Printf("done: %d definitions seeded to s3://%s/%s/\n", seeded, bucket, *prefix)
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed-definitions: "+format+"\n", a...)
	os.Exit(1)
}
