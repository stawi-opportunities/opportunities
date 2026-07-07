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
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcwait "github.com/testcontainers/testcontainers-go/wait"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/billing"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/frontier"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
	"github.com/stawi-opportunities/opportunities/pkg/savedjobs"
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

// PostgresContainerNoMigrate boots TimescaleDB + pgvector (the official ha image
// ships both) and returns a connected *sql.DB without applying any migrations.
//
// Use this when you need to interleave database setup steps (e.g., creating stub tables
// before running migrations). The container is torn down via t.Cleanup.
func PostgresContainerNoMigrate(t *testing.T, ctx context.Context) *sql.DB {
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

	return db
}

// ApplyCapabilitySQLDir applies every *.sql file in the given directory in
// numeric order. These files are limited to PostgreSQL capabilities GORM cannot
// express; ordinary table shape is created by AutoMigrate.
func ApplyCapabilitySQLDir(t *testing.T, ctx context.Context, db *sql.DB, dir string) {
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

// ApplyGreenfieldSchema mirrors production migration jobs: GORM creates
// ordinary PostgreSQL tables, then the app-specific capability SQL enables
// TimescaleDB hypertables, pgvector indexes, append-only triggers, partial
// indexes, and continuous aggregates.
func ApplyGreenfieldSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)

	models := []any{
		&domain.Source{},
		&domain.CrawlJob{},
		&repository.SourceRecipe{},
		&domain.Company{},
		&domain.CrawlRun{},
		&jobqueue.QueueRecord{},
		&jobqueue.OpportunityRecord{},
		&jobqueue.OpportunityIdentityRecord{},
		&jobqueue.OpportunitySourceRecord{},
		&jobqueue.IngestEventRecord{},
		&domain.CandidateProfile{},
		&domain.CandidateApplication{},
		&domain.OpportunityFlag{},
	}
	models = append(models, frontier.Schema()...)
	models = append(models, matching.Schema()...)
	models = append(models, applications.Schema()...)
	models = append(models, savedjobs.Schema()...)
	models = append(models, billing.Schema()...)
	require.NoError(t, g.AutoMigrate(models...))

	root := repoRoot(t)
	ApplyCapabilitySQLDir(t, ctx, db, filepath.Join(root, "apps", "crawler", "migrations", "0001"))
	ApplyCapabilitySQLDir(t, ctx, db, filepath.Join(root, "apps", "matching", "migrations", "0001"))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// PostgresContainer boots TimescaleDB + pgvector (the official ha image
// ships both) and applies the greenfield GORM + capability schema.
// Returns a *sql.DB connected to the freshly migrated database.
//
// Use this in any integration suite that needs a clean DB with the
// project's schema applied. The container is torn down via t.Cleanup.
func PostgresContainer(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	db := PostgresContainerNoMigrate(t, ctx)
	ApplyGreenfieldSchema(t, ctx, db)
	return db
}
