package kv_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"stawi.jobs/pkg/kv"
)

func startRedis(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "6379/tcp")
	url := fmt.Sprintf("redis://%s:%s/0", host, port.Port())
	return url, func() { _ = c.Terminate(context.Background()) }
}

func TestDedupGetSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	url, stop := startRedis(t, ctx)
	defer stop()

	client, err := kv.Open(kv.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	dd := kv.NewDedupStore(client)

	// Miss
	got, ok, err := dd.Get(ctx, "src|extA")
	if err != nil {
		t.Fatalf("get miss: %v", err)
	}
	if ok {
		t.Fatalf("expected miss, got %q", got)
	}

	// Set + hit
	if err := dd.Set(ctx, "src|extA", "clu_1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err = dd.Get(ctx, "src|extA")
	if err != nil || !ok || got != "clu_1" {
		t.Fatalf("expected hit clu_1, got ok=%v val=%q err=%v", ok, got, err)
	}
}

func TestClusterGetSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	url, stop := startRedis(t, ctx)
	defer stop()

	client, err := kv.Open(kv.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	cs := kv.NewClusterStore(client)

	snap := kv.ClusterSnapshot{
		ClusterID: "clu_1",
		Title:     "Backend Engineer",
		Company:   "Acme",
	}
	if err := cs.Set(ctx, snap); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, ok, err := cs.Get(ctx, "clu_1")
	if err != nil || !ok {
		t.Fatalf("expected hit, got ok=%v err=%v", ok, err)
	}
	if got.Title != "Backend Engineer" {
		t.Fatalf("round-trip lost: %+v", got)
	}
}
