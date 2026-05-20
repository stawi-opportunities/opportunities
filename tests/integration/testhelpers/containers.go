//go:build integration

// Package testhelpers provides shared testcontainer setup helpers for
// the stawi-opportunities/opportunities integration test suite.
//
// Each helper starts a real container via testcontainers-go and returns
// the connection string / client so the test can wire services.
// All helpers call t.Cleanup to stop the container when the test ends.
package testhelpers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

// MinIOContainer starts a MinIO server and returns its S3-compatible endpoint URL.
// Uses: testcontainers-go/modules/minio (already in go.mod).
func MinIOContainer(t *testing.T, ctx context.Context) (endpoint, accessKey, secretKey string) {
	t.Helper()
	mc, err := tcminio.Run(ctx, "minio/minio:latest")
	require.NoError(t, err, "start minio container")
	t.Cleanup(func() { _ = mc.Terminate(ctx) })

	ep, err := mc.ConnectionString(ctx)
	require.NoError(t, err, "minio connection string")
	return ep, "minioadmin", "minioadmin"
}

// ManticoreContainer starts a Manticore Search server and returns its
// HTTP JSON API URL (port 9308).
// Image: manticoresearch/manticore:6.2.0
//
// TODO: pin image version once the cluster's Manticore version is confirmed.
func ManticoreContainer(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "manticoresearch/manticore:6.2.0",
		ExposedPorts: []string{"9308/tcp", "9306/tcp"},
		WaitingFor: tcwait.ForAll(
			tcwait.ForListeningPort("9308/tcp").WithStartupTimeout(60*time.Second),
		),
		Env: map[string]string{
			"MANTICORE_RT_MEM_LIMIT": "128M",
		},
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start manticore container")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "9308")
	require.NoError(t, err)
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// NATSContainer starts a NATS server with JetStream enabled and returns
// its NATS connection URL (nats://host:port).
func NATSContainer(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "nats:2.10-alpine",
		Cmd:          []string{"-js"},
		ExposedPorts: []string{"4222/tcp"},
		WaitingFor:   tcwait.ForListeningPort("4222/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start nats container")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "4222")
	require.NoError(t, err)
	return fmt.Sprintf("nats://%s:%s", host, port.Port())
}

// ValkeyContainer starts a Valkey server and returns its Redis-compatible
// connection URL (redis://host:port).
func ValkeyContainer(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "valkey/valkey:7.2-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   tcwait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start valkey container")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "6379")
	require.NoError(t, err)
	return fmt.Sprintf("redis://%s:%s", host, port.Port())
}

// PostgresContainer boots TimescaleDB + pgvector (the official ha image
// ships both) and applies every db/migrations/*.sql in numeric order.
// Returns a *sql.DB connected to the freshly migrated database.
//
// Use this in any integration suite that needs a clean DB with the
// project's schema applied. The container is torn down via t.Cleanup.
func PostgresContainer(t *testing.T, ctx context.Context, migrationsDir string) *sql.DB {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "timescale/timescaledb-ha:pg16",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "opportunities_test",
		},
		WaitingFor: tcwait.ForListeningPort("5432/tcp").
			WithStartupTimeout(90 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)

	dsn := fmt.Sprintf("postgres://test:test@%s:%s/opportunities_test?sslmode=disable",
		host, port.Port())
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Eventually(t, func() bool { return db.PingContext(ctx) == nil },
		60*time.Second, 250*time.Millisecond, "postgres ping")

	applyMigrations(t, ctx, db, migrationsDir)
	return db
}

func applyMigrations(t *testing.T, ctx context.Context, db *sql.DB, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "read migrations dir %s", dir)

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, "read %s", name)
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err, "apply %s", name)
	}
}
