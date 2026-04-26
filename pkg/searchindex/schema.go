package searchindex

import (
	"context"
	"strings"
)

// idxJobsRTDDL is the schema for the primary job search index.
// Embedding dimension is pinned to 1536 (OpenAI text-embedding-3-small)
// for Phase 2; changing it is a rebuild, not a migration.
//
// Single-line form — Manticore's /sql endpoint accepts newlines, but
// keeping it one line makes query-string escaping trivial.
const idxJobsRTDDL = `CREATE TABLE idx_opportunities_rt (canonical_id string attribute, slug string attribute, title text indexed, company text indexed, description text indexed stored, location_text text indexed, category string attribute, country string attribute, language string attribute, remote_type string attribute, employment_type string attribute, seniority string attribute, salary_min uint, salary_max uint, currency string attribute, quality_score float, is_featured bool, posted_at timestamp, last_seen_at timestamp, expires_at timestamp, status string attribute, embedding float_vector knn_type='hnsw' knn_dims='1536' hnsw_similarity='COSINE', embedding_model string attribute)`

// Apply creates idx_opportunities_rt if it does not exist. Idempotent — on
// re-run it swallows Manticore's "table already exists" error which
// the HTTP /sql endpoint returns as an error JSON payload.
func Apply(ctx context.Context, c *Client) error {
	_, err := c.SQL(ctx, idxJobsRTDDL)
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return nil
	}
	return err
}
