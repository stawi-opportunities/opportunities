package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"stawi.jobs/pkg/searchindex"
)

func startManticoreForAPITest(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "manticoresearch/manticore:6.3.2",
		ExposedPorts: []string{"9308/tcp"},
		Env:          map[string]string{"EXTRA": "1"},
		WaitingFor:   wait.ForListeningPort("9308/tcp").WithStartupTimeout(120 * time.Second),
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

func TestSearchV2HandlerReturnsManticoreRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	url, stop := startManticoreForAPITest(t, ctx)
	defer stop()

	client, err := searchindex.Open(searchindex.Config{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := searchindex.Apply(ctx, client); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Seed one row via /replace.
	now := time.Now().Unix()
	doc := map[string]any{
		"canonical_id":    "can_api_1",
		"slug":            "senior-backend-acme",
		"title":           "Senior Backend Engineer",
		"company":         "Acme",
		"description":     "We are hiring",
		"category":        "programming",
		"country":         "KE",
		"language":        "en",
		"remote_type":     "remote",
		"employment_type": "full-time",
		"seniority":       "senior",
		"salary_min":      100000,
		"salary_max":      180000,
		"currency":        "USD",
		"quality_score":   float32(85),
		"is_featured":     true,
		"posted_at":       now,
		"last_seen_at":    now,
		"expires_at":      now + 86400,
		"status":          "active",
	}
	if err := client.Replace(ctx, "idx_jobs_rt", 1, doc); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handler := searchV2Handler(client)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/search?q=backend&country=KE", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp searchV2Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Total != 1 {
		t.Fatalf("total=%d want 1, body=%s", resp.Total, rec.Body.String())
	}
	if resp.Hits[0].CanonicalID != "can_api_1" {
		t.Fatalf("hit mismatch: %+v", resp.Hits[0])
	}
}
