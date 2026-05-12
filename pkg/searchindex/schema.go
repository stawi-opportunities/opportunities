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
// ALTER TABLE in this same function.
//
// DDL is always unqualified. Manticore's parser rejects
// `CREATE TABLE cluster:name (...)` ("syntax error near ':name'"); the
// cluster-aware path is to create the table bare and then `ALTER
// CLUSTER name ADD table_name` to enrol it in Galera replication. Only
// DML (Replace/Update/DeleteWhere/Bulk) uses the cluster:table form,
// which Client.qualify applies automatically.
func Apply(ctx context.Context, c *Client) error {
	ddl := fmt.Sprintf(idxOpportunitiesRTDDL, idxOpportunitiesRT)
	if _, err := c.SQL(ctx, ddl); err != nil && !isAlreadyExists(err) {
		return err
	}
	// Additive column migrations. `source_id` was added after the
	// initial DDL shipped, so existing installations need an ALTER
	// TABLE; "duplicate column" / "already in schema" is the success
	// signal on re-runs.
	alter := fmt.Sprintf("ALTER TABLE %s ADD COLUMN source_id bigint", idxOpportunitiesRT)
	if _, alterErr := c.SQL(ctx, alter); alterErr != nil {
		if !isAlreadyExists(alterErr) && !isDuplicateColumn(alterErr) {
			return alterErr
		}
	}
	// Enrol the table into the Galera cluster when configured. ALTER
	// CLUSTER ADD is a no-op when the table is already a member; we
	// only fail on errors that aren't already-a-member.
	if c.cluster != "" {
		add := fmt.Sprintf("ALTER CLUSTER %s ADD %s", c.cluster, idxOpportunitiesRT)
		if _, addErr := c.SQL(ctx, add); addErr != nil {
			if !isAlreadyExists(addErr) && !isDuplicateColumn(addErr) {
				return fmt.Errorf("alter cluster add: %w", addErr)
			}
		}
	}
	return nil
}

// isDuplicateColumn matches Manticore's "duplicate column" error from
// repeated ALTER TABLE ADD COLUMN calls. Manticore phrases it as
// "already in schema" in 25.x and "duplicate column" in older builds.
// Also matches the 25.x "ALTER is not supported for tables in cluster"
// response — when the table is already a Galera member the column was
// either created with the initial DDL or added by an earlier pass on
// another replica that propagated via Galera, so re-trying is a no-op.
func isDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already in schema") ||
		strings.Contains(msg, "alter is not supported for tables in cluster")
}

// isAlreadyExists matches Manticore's "already exists / already part of"
// family of idempotency errors — surfaced for CREATE TABLE, ALTER
// CLUSTER ADD against an already-attached table, etc.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already part of")
}
