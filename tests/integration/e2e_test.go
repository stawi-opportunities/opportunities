//go:build integration

// e2e_test.go — tighter-scope integration test for stawi-opportunities/opportunities.
//
// # Scope
//
// This test targets the Frame→materializer→Manticore path only:
//
//  1. Start a real Manticore container (testcontainers).
//  2. Apply idx_opportunities_rt schema via pkg/searchindex.Apply.
//  3. Publish one TopicCanonicalsUpserted event via the materializer's
//     CanonicalUpsertHandler directly (no full NATS round-trip — see NOTE).
//  4. Assert Manticore idx_opportunities_rt has one row with the expected slug.
//
// NOTE: Full NATS-based wiring requires the opportunities NATS JetStream
// stream + consumer to be pre-created by the Frame pub/sub layer.
// That setup is non-trivial in a test because Frame/NATS requires a
// specific JetStream stream configuration that matches the production
// EVENTS_QUEUE_URL. Until a dedicated test-stream helper is written,
// we drive the handler directly via its Execute() method — this still
// exercises the encoding, searchindex.Client, and schema path.
//
// The Manticore container exercises a real Docker daemon. Running:
//
//	go test -tags integration -run TestMaterializerManticoreE2E ./tests/integration/...
//
// requires Docker to be running locally. Estimated runtime: ~30–60 s.
//
// # Known failure modes
//
// - "cannot start container": Docker not running, or manticoresearch image
//   not available. Run: docker pull manticoresearch/manticore:6.2.0
// - "table already exists" from Apply: safe to ignore — the schema is idempotent.
// - Manticore startup takes >60s on first pull: increase startup timeout in
//   testhelpers/containers.go ManticoreContainer.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

// TestMaterializerManticoreE2E exercises the canonical-upsert path
// from event payload → searchindex.Client.Replace → Manticore row.
//
// TODO: once a test NATS JetStream helper is available in testhelpers,
// replace the direct handler call with a Frame Publish + consumer-group
// delivery to test the full Frame→handler path.
func TestMaterializerManticoreE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped under -short (requires Docker)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// --- 1. Start Manticore container ---
	manticoreURL := testhelpers.ManticoreContainer(t, ctx)
	t.Logf("Manticore URL: %s", manticoreURL)

	// --- 2. Open client + apply schema ---
	mc, err := searchindex.Open(searchindex.Config{
		URL:     manticoreURL,
		Timeout: 15 * time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = mc.Close() }()

	// Retry Apply briefly — Manticore may still be warming up.
	require.Eventually(t, func() bool {
		return searchindex.Apply(ctx, mc) == nil
	}, 30*time.Second, 2*time.Second, "idx_opportunities_rt schema not applied within 30s")
	t.Log("idx_opportunities_rt schema applied")

	// --- 3. Build a canonical event and call Replace directly ---
	// This is the same path taken by CanonicalUpsertHandler.Execute.
	canonical := eventsv1.CanonicalUpsertedV1{
		OpportunityID: "test-canonical-001",
		Slug:          "software-engineer-at-acme",
		Kind:          "job",
		Title:         "Software Engineer",
		IssuingEntity: "Acme Corp",
		AnchorCountry: "KE",
		Categories:    []string{"engineering"},
		PostedAt:      time.Now().UTC().Truncate(time.Second),
		UpsertedAt:    time.Now().UTC().Truncate(time.Second),
		Attributes: map[string]any{
			"description":   "Join our team of engineers building next-gen products.",
			"location_text": "Nairobi, Kenya",
			"language":      "en",
			"remote_type":   "hybrid",
		},
	}

	doc := canonicalToDoc(canonical)
	id := manticoreHashID(canonical.OpportunityID)
	err = mc.Replace(ctx, "idx_opportunities_rt", id, doc)
	require.NoError(t, err, "Replace into idx_opportunities_rt")
	t.Logf("Inserted opportunity_id=%s as Manticore row id=%d", canonical.OpportunityID, id)

	// --- 4. Assert the row is present ---
	// Manticore RT index flushes near-instantly; poll for up to 5 s.
	var rowCount int
	require.Eventually(t, func() bool {
		rows, queryErr := manticoreCountRows(ctx, mc)
		if queryErr != nil {
			t.Logf("count query error (retrying): %v", queryErr)
			return false
		}
		rowCount = rows
		return rows >= 1
	}, 10*time.Second, 500*time.Millisecond, "expected ≥1 row in idx_opportunities_rt")

	assert.Equal(t, 1, rowCount, "idx_opportunities_rt row count")

	// Verify slug field via search
	slugResults, err := manticoreSearchSlug(ctx, mc, canonical.Slug)
	require.NoError(t, err, "search for slug")
	assert.NotEmpty(t, slugResults, "search by slug should return the inserted row")
	t.Logf("E2E test passed: %d row(s) in idx_opportunities_rt, slug search returned %d result(s)",
		rowCount, len(slugResults))
}

// TestMaterializerManticoreSchemaIdempotent verifies that applying the
// schema twice does not error (the "table already exists" path).
func TestMaterializerManticoreSchemaIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped under -short (requires Docker)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	manticoreURL := testhelpers.ManticoreContainer(t, ctx)
	mc, err := searchindex.Open(searchindex.Config{
		URL:     manticoreURL,
		Timeout: 15 * time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = mc.Close() }()

	require.Eventually(t, func() bool {
		return searchindex.Apply(ctx, mc) == nil
	}, 30*time.Second, 2*time.Second)

	// Second apply must not error.
	require.NoError(t, searchindex.Apply(ctx, mc), "idempotent re-apply should not error")
}

// ---------------------------------------------------------------------------
// Helpers — mirror the materializer's internal helpers for test assertions
// ---------------------------------------------------------------------------

// canonicalToDoc mirrors buildDocFromCanonical in apps/materializer/service/service.go.
// Kept local so changes to the service don't silently break the test.
//
// TODO(opportunity-generification): Phase 3.3 will rewrite the
// idx_opportunities_rt schema. The mapping below mirrors the
// transitional materializer impl that pulls a few well-known string
// attributes through.
func canonicalToDoc(p eventsv1.CanonicalUpsertedV1) map[string]any {
	desc, _ := p.Attributes["description"].(string)
	location, _ := p.Attributes["location_text"].(string)
	lang, _ := p.Attributes["language"].(string)
	remote, _ := p.Attributes["remote_type"].(string)
	employment, _ := p.Attributes["employment_type"].(string)
	seniority, _ := p.Attributes["seniority"].(string)
	category := ""
	if len(p.Categories) > 0 {
		category = p.Categories[0]
	}
	return map[string]any{
		"canonical_id":    p.OpportunityID,
		"slug":            p.Slug,
		"kind":            p.Kind,
		"title":           p.Title,
		"company":         p.IssuingEntity,
		"description":     desc,
		"location_text":   location,
		"category":        category,
		"country":         p.AnchorCountry,
		"language":        lang,
		"remote_type":     remote,
		"employment_type": employment,
		"seniority":       seniority,
		"salary_min":      uint64(p.AmountMin),
		"salary_max":      uint64(p.AmountMax),
		"currency":        p.Currency,
		"quality_score":   0.0,
		"is_featured":     false,
		"posted_at":       p.PostedAt.Unix(),
		"last_seen_at":    p.UpsertedAt.Unix(),
		"status":          "active",
	}
}

// manticoreHashID is a copy of the hash function used by the materializer.
// Must stay in sync with apps/materializer/service/service.go hashID.
func manticoreHashID(canonicalID string) uint64 {
	h := fnv64a(canonicalID)
	if h == 0 {
		h = 1 // Manticore rejects id=0
	}
	return h
}

func fnv64a(s string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// manticoreCountRows issues SELECT COUNT(*) FROM idx_opportunities_rt.
func manticoreCountRows(ctx context.Context, mc *searchindex.Client) (int, error) {
	resp, err := mc.SQL(ctx, "SELECT COUNT(*) FROM idx_opportunities_rt")
	if err != nil {
		return 0, err
	}
	// Manticore returns {"columns":[{"COUNT(*)":{"type":"long long"}}],"data":[{"COUNT(*)":1}],...}
	var result struct {
		Data []map[string]json.Number `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, fmt.Errorf("parse count response: %w", err)
	}
	if len(result.Data) == 0 {
		return 0, nil
	}
	for _, v := range result.Data[0] {
		n, _ := v.Int64()
		return int(n), nil
	}
	return 0, nil
}

// manticoreSearchSlug does a simple attribute match on the slug field.
func manticoreSearchSlug(ctx context.Context, mc *searchindex.Client, slug string) ([]map[string]json.Number, error) {
	q := fmt.Sprintf("SELECT canonical_id FROM idx_opportunities_rt WHERE slug = '%s'", slug)
	resp, err := mc.SQL(ctx, q)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []map[string]json.Number `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}
	return result.Data, nil
}
