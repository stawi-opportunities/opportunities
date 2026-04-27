package searchindex

import (
	"context"
	"strings"
)

// idxOpportunitiesRTDDL is the schema for the polymorphic opportunity
// search index. Universal columns describe every opportunity (job,
// scholarship, tender, deal, funding); per-kind sparse facet columns
// surface kind-specific filters without requiring a discriminated union.
//
// Embedding dimension is pinned to 384 (sentence-transformers/all-MiniLM-L6-v2)
// for the polymorphic index; changing it is a rebuild, not a migration.
//
// Manticore 6.3 parser notes: numeric/bool/timestamp columns are stored
// attributes by default, so the explicit `attribute` keyword is reserved
// for `string attribute` (and is rejected after `float`/`bool`/`timestamp`).
//
// Single-line form — Manticore's /sql endpoint accepts newlines, but
// keeping it one line makes query-string escaping trivial.
const idxOpportunitiesRTDDL = `CREATE TABLE idx_opportunities_rt (` +
	// Discriminator
	`kind string attribute,` +
	// Universal indexable text + categories
	`title text indexed,` +
	`description text indexed,` +
	`issuing_entity text indexed,` +
	`categories multi64,` +
	// Universal location
	`country string attribute,` +
	`region string attribute,` +
	`city string attribute,` +
	`lat float,` +
	`lon float,` +
	`remote bool,` +
	`geo_scope string attribute,` +
	// Universal time
	`posted_at timestamp,` +
	`deadline timestamp,` +
	// Universal monetary
	`amount_min float,` +
	`amount_max float,` +
	`currency string attribute,` +
	// Sparse per-kind facet columns
	`employment_type string attribute,` +
	`seniority string attribute,` +
	`field_of_study string attribute,` +
	`degree_level string attribute,` +
	`procurement_domain string attribute,` +
	`funding_focus string attribute,` +
	`discount_percent float,` +
	// Embedding
	`embedding float_vector knn_type='hnsw' knn_dims='384' hnsw_similarity='cosine'` +
	`)`

// Apply makes idx_opportunities_rt exist. Idempotent — repeated calls
// swallow Manticore's "table already exists" error. Future evolutions
// (new sparse facet columns added by future kind YAMLs) will land via
// ALTER TABLE in this same function.
func Apply(ctx context.Context, c *Client) error {
	_, err := c.SQL(ctx, idxOpportunitiesRTDDL)
	if err != nil && !isAlreadyExists(err) {
		return err
	}
	// Future: ALTER TABLE statements for newly-introduced sparse columns
	// based on the kind registry. For now, the seed kinds' columns are
	// all present in idxOpportunitiesRTDDL.
	return nil
}

// isAlreadyExists matches Manticore's "table already exists" error.
// The /sql endpoint surfaces this as an error JSON whose message
// contains the substring "already exists" (case-insensitive).
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}
