//go:build integration

package service

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// valkeyHarness holds a running Valkey container and a go-redis client.
type valkeyHarness struct {
	Client    *redis.Client
	container testcontainers.Container
}

func (h *valkeyHarness) Close() { _ = h.container.Terminate(context.Background()) }

func startValkey(t *testing.T) *valkeyHarness {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "valkey/valkey:7.2",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "6379")
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: host + ":" + port.Port()})
	require.NoError(t, client.Ping(ctx).Err())
	return &valkeyHarness{Client: client, container: c}
}

// TestKVRebuild_Integration is a placeholder for Wave 7 testcontainer
// wiring once a local Iceberg catalog can be spun up in CI.
//
// TODO(wave7): wire a testcontainer-backed Iceberg catalog + MinIO,
// seed jobs.canonicals_current with fixture rows including cluster_id,
// and assert:
//   - res.Rows equals total rows seeded
//   - res.ClusterKeysSet equals rows with non-empty cluster_id
//   - cluster:{id} keys in Valkey contain the expected canonical_id
//   - rows with empty cluster_id do not create any key
func TestKVRebuild_Integration(t *testing.T) {
	t.Skip("TODO(wave7): requires Iceberg catalog testcontainer")

	ctx := context.Background()
	kv := startValkey(t)
	defer kv.Close()

	// cat would be constructed from a testcontainer catalog in Wave 7.
	// r := NewKVRebuilder(cat, kv.Client)
	// res, err := r.Run(ctx)
	// require.NoError(t, err)
	// require.Equal(t, 2, res.Rows)
	// require.Equal(t, 2, res.ClusterKeysSet)

	_ = ctx
}
