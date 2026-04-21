//go:build integration

package archive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
)

// newR2ArchiveWithEndpoint points the client at an arbitrary
// S3-compatible endpoint (minio in CI). Uses path-style addressing
// because minio rejects virtual-host-style for bucket operations by
// default.
func newR2ArchiveWithEndpoint(endpoint, accessKey, secretKey, bucket string) *R2Archive {
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
	})
	return &R2Archive{client: client, bucket: bucket}
}

// startMinio spins up a minio container via testcontainers and
// returns its S3-compatible endpoint. Cleanup runs on test exit.
func startMinio(ctx context.Context, t *testing.T) (endpoint, access, secret string) {
	t.Helper()
	c, err := minio.Run(ctx, "minio/minio:latest",
		minio.WithUsername("minio"),
		minio.WithPassword("minio123"),
	)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	ep, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return "http://" + ep, "minio", "minio123"
}

// newTestR2Archive builds an R2Archive pointed at the given minio
// endpoint and eagerly creates the bucket. Returns the ready-to-use
// archive.
func newTestR2Archive(t *testing.T, endpoint, access, secret, bucket string) *R2Archive {
	t.Helper()
	a := newR2ArchiveWithEndpoint(endpoint, access, secret, bucket)
	_, err := a.client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("create bucket %q: %v", bucket, err)
	}
	return a
}

func TestR2Archive_RoundTripAgainstMinio(t *testing.T) {
	ctx := context.Background()
	endpoint, access, secret := startMinio(ctx, t)
	arch := newTestR2Archive(t, endpoint, access, secret, "test-archive")

	body := []byte("<html>hi</html>")
	hash, size, err := arch.PutRaw(ctx, body)
	if err != nil {
		t.Fatalf("PutRaw: %v", err)
	}
	if size != int64(len(body)) {
		t.Errorf("size = %d, want %d", size, len(body))
	}

	// Round-trip: Get should return the same bytes (gzip transparent).
	got, err := arch.GetRaw(ctx, hash)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch: got %q, want %q", got, body)
	}

	// Has should be true for a known hash.
	has, err := arch.HasRaw(ctx, hash)
	if err != nil || !has {
		t.Errorf("HasRaw = (%v, %v), want (true, nil)", has, err)
	}

	// Has should be false for an unknown hash.
	has, err = arch.HasRaw(ctx, "deadbeef")
	if err != nil || has {
		t.Errorf("HasRaw unknown = (%v, %v), want (false, nil)", has, err)
	}

	// GetRaw for unknown hash should return ErrNotFound.
	if _, err := arch.GetRaw(ctx, "deadbeef"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRaw unknown err = %v, want ErrNotFound", err)
	}
}

func TestR2Archive_ClusterBundleRoundTrip(t *testing.T) {
	ctx := context.Background()
	endpoint, access, secret := startMinio(ctx, t)
	arch := newTestR2Archive(t, endpoint, access, secret, "test-archive")

	now := time.Now().UTC().Truncate(time.Second)

	snap := CanonicalSnapshot{
		ID:        "cr7qs3q8j1hci9fn3sag",
		ClusterID: "cr7qs3q8j1hci9fn3sag",
		Slug:      "backend-engineer-at-acme-a3f7b2",
		Title:     "Backend Engineer",
		WrittenAt: now,
	}
	if err := arch.PutCanonical(ctx, snap.ClusterID, snap); err != nil {
		t.Fatalf("PutCanonical: %v", err)
	}
	gotSnap, err := arch.GetCanonical(ctx, snap.ClusterID)
	if err != nil {
		t.Fatalf("GetCanonical: %v", err)
	}
	if gotSnap.Slug != snap.Slug || gotSnap.Title != snap.Title {
		t.Errorf("GetCanonical roundtrip mismatch: got %+v", gotSnap)
	}

	blob := VariantBlob{
		ID:             "cr7qr2q8j1hci9fn3sbg",
		ClusterID:      snap.ClusterID,
		SourceID:       "src-1",
		Markdown:       "# Backend Engineer",
		RawContentHash: "a3f7b2c4",
		WrittenAt:      now,
	}
	if err := arch.PutVariant(ctx, snap.ClusterID, blob.ID, blob); err != nil {
		t.Fatalf("PutVariant: %v", err)
	}
	gotBlob, err := arch.GetVariant(ctx, snap.ClusterID, blob.ID)
	if err != nil {
		t.Fatalf("GetVariant: %v", err)
	}
	if gotBlob.Markdown != blob.Markdown {
		t.Errorf("GetVariant markdown mismatch: got %q", gotBlob.Markdown)
	}

	manifest := Manifest{
		ClusterID:   snap.ClusterID,
		CanonicalID: snap.ID,
		Slug:        snap.Slug,
		Variants: []ManifestVariant{
			{VariantID: blob.ID, SourceID: blob.SourceID, RawContentHash: blob.RawContentHash, ScrapedAt: now},
		},
		UpdatedAt: now,
	}
	if err := arch.PutManifest(ctx, snap.ClusterID, manifest); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	gotM, err := arch.GetManifest(ctx, snap.ClusterID)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(gotM.Variants) != 1 || gotM.Variants[0].VariantID != blob.ID {
		t.Errorf("GetManifest variants mismatch: %+v", gotM.Variants)
	}
}

func TestR2Archive_DeleteClusterRemovesAllKeys(t *testing.T) {
	ctx := context.Background()
	endpoint, access, secret := startMinio(ctx, t)
	arch := newTestR2Archive(t, endpoint, access, secret, "test-archive")

	clusterID := "cr7qs3q8j1hci9fn3sag"
	_ = arch.PutCanonical(ctx, clusterID, CanonicalSnapshot{ID: clusterID, ClusterID: clusterID})
	_ = arch.PutVariant(ctx, clusterID, "v1", VariantBlob{ID: "v1", ClusterID: clusterID})
	_ = arch.PutVariant(ctx, clusterID, "v2", VariantBlob{ID: "v2", ClusterID: clusterID})
	_ = arch.PutManifest(ctx, clusterID, Manifest{ClusterID: clusterID, CanonicalID: clusterID})

	if err := arch.DeleteCluster(ctx, clusterID); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	// All cluster objects should now be gone.
	if _, err := arch.GetCanonical(ctx, clusterID); !errors.Is(err, ErrNotFound) {
		t.Errorf("canonical still present after delete: err=%v", err)
	}
	if _, err := arch.GetVariant(ctx, clusterID, "v1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("variant v1 still present after delete: err=%v", err)
	}
	if _, err := arch.GetManifest(ctx, clusterID); !errors.Is(err, ErrNotFound) {
		t.Errorf("manifest still present after delete: err=%v", err)
	}
}
