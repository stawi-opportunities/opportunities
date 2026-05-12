package searchindex

import (
	"context"
	"fmt"
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
// idxOpportunitiesRT is the search-index table name. Apply() qualifies
// it with the cluster prefix when the Client is configured for
// replication; callers that emit DML (Replace/Update/DeleteWhere) get
// qualification automatically via Client.qualify.
const idxOpportunitiesRT = "idx_opportunities_rt"

const idxOpportunitiesRTDDL = `CREATE TABLE %s (` +
	// Provenance — `bigint` of hashID(source_id). Used by
	// /admin/sources/stop in the crawler to DELETE all docs from a
	// stopped source in one Manticore statement. Indexed as an
	// attribute so equality filters are O(log n).
	`source_id bigint,` +
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
// ALTER TABLE in this same function. When the Client is configured for
// replication (Client.Cluster() non-empty) the DDL is qualified with
// the cluster prefix so the table participates in Galera replication
// across all replicas; bare table names only replicate locally.
func Apply(ctx context.Context, c *Client) error {
	qualified := c.qualify(idxOpportunitiesRT)
	ddl := fmt.Sprintf(idxOpportunitiesRTDDL, qualified)
	_, err := c.SQL(ctx, ddl)
	if err != nil && !isAlreadyExists(err) {
		return err
	}
	// Best-effort migration for legacy single-node installs where the
	// table was originally created outside any cluster. Fresh installs
	// using CREATE TABLE cluster:name already participate; this statement
	// returns "already a member" or "no such table" in steady state, so
	// we discard the error either way.
	if c.cluster != "" {
		_, _ = c.SQL(ctx, fmt.Sprintf("ALTER CLUSTER %s ADD %s", c.cluster, idxOpportunitiesRT))
	}
	// Additive column migrations land here. `source_id` was added after
	// idx_opportunities_rt shipped, so existing installations need an
	// ALTER TABLE; "duplicate column" / "already in schema" is the
	// success signal on re-runs.
	alterStmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN source_id bigint", qualified)
	if _, alterErr := c.SQL(ctx, alterStmt); alterErr != nil {
		if !isAlreadyExists(alterErr) && !isDuplicateColumn(alterErr) {
			return alterErr
		}
	}
	return nil
}

// isDuplicateColumn matches Manticore's "duplicate column" error from
// repeated ALTER TABLE ADD COLUMN calls. Manticore phrases it as
// "already in schema" in 25.x and "duplicate column" in older builds.
func isDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already in schema")
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
