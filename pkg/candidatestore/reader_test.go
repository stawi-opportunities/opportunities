//go:build integration

// Integration tests for candidatestore.Reader against a live Iceberg
// catalog backed by MinIO + Postgres. These tests require Wave 7's
// testing scaffolding (iceberg-ops testcontainer helpers) before they
// can be wired up; stubs are kept here so the build tag is established
// and CI skips them correctly.
//
// TODO(wave7): replace stubs with real Iceberg-against-MinIO tests once
// the testcontainer helpers from apps/iceberg-ops/testutil are available.
package candidatestore

import (
	"context"
	"testing"
	"time"
)

func TestReaderReturnsLatestEmbedding(t *testing.T) {
	t.Skip("Wave 7 Iceberg testcontainer scaffolding not yet available")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_ = ctx
}

func TestReaderReturnsErrNotFoundWhenNoEmbedding(t *testing.T) {
	t.Skip("Wave 7 Iceberg testcontainer scaffolding not yet available")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_ = ctx
}
