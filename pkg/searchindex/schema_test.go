//go:build integration

package searchindex_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

// startManticore boots a Manticore container and returns the HTTP
// URL (e.g. "http://127.0.0.1:42719") + a stop function.
func startManticore(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "manticoresearch/manticore:6.3.2",
		ExposedPorts: []string{"9308/tcp"},
		// EXTRA=1 installs the columnar library (required for KNN/HNSW
		// float_vector columns). The download adds ~10 s on first boot.
		Env: map[string]string{"EXTRA": "1"},
		WaitingFor: wait.ForListeningPort("9308/tcp").WithStartupTimeout(120 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start manticore: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "9308/tcp")
	url := fmt.Sprintf("http://%s:%s", host, port.Port())
	return url, func() { _ = c.Terminate(context.Background()) }
}

func TestApplySchemaIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	url, stop := startManticore(t, ctx)
	defer stop()

	client, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if err := searchindex.Apply(ctx, client); err != nil {
		t.Fatalf("apply #1: %v", err)
	}
	if err := searchindex.Apply(ctx, client); err != nil {
		t.Fatalf("apply #2 (must be idempotent): %v", err)
	}

	raw, err := client.SQL(ctx, "SHOW TABLES")
	if err != nil {
		t.Fatalf("show tables: %v", err)
	}
	if !strings.Contains(string(raw), "idx_opportunities_rt") {
		t.Fatalf("idx_opportunities_rt not present after Apply — SHOW TABLES returned:\n%s", string(raw))
	}
}
