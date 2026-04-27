// schemas.go — Go-native iceberg.Schema definitions for every table the
// writer persists to. Mirrors the historical Python source-of-truth in
// definitions/iceberg/_schemas.py one-for-one (column names, types,
// field IDs, required/optional, list element IDs).
//
// These are now the source of truth. The bootstrap-iceberg subcommand
// in apps/writer uses them to CreateTable() against Lakekeeper; the
// _schemas.py file is kept around for analyst documentation only.

package icebergclient

import (
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/table"
)

// req returns a required NestedField.
func req(id int, name string, t iceberg.Type) iceberg.NestedField {
	return iceberg.NestedField{ID: id, Name: name, Type: t, Required: true}
}

// opt returns an optional NestedField.
func opt(id int, name string, t iceberg.Type) iceberg.NestedField {
	return iceberg.NestedField{ID: id, Name: name, Type: t, Required: false}
}

// strList returns ListType<String> (element required).
func strList(elementID int) *iceberg.ListType {
	return &iceberg.ListType{
		ElementID:       elementID,
		Element:         iceberg.StringType{},
		ElementRequired: true,
	}
}

// f64List returns ListType<Float64> (element required) — matches the
// pyiceberg DoubleType list element shape.
func f64List(elementID int) *iceberg.ListType {
	return &iceberg.ListType{
		ElementID:       elementID,
		Element:         iceberg.Float64Type{},
		ElementRequired: true,
	}
}

// f32List returns ListType<Float32> (element required) — used by
// candidate embeddings (wire format is []float32).
func f32List(elementID int) *iceberg.ListType {
	return &iceberg.ListType{
		ElementID:       elementID,
		Element:         iceberg.Float32Type{},
		ElementRequired: true,
	}
}

// envelope returns the standard {event_id, occurred_at} tail. Mirrors
// the _envelope helper in _schemas.py.
func envelope(baseID int) []iceberg.NestedField {
	return []iceberg.NestedField{
		req(baseID, "event_id", iceberg.StringType{}),
		req(baseID+1, "occurred_at", iceberg.TimestampTzType{}),
	}
}

// ----------------------------------------------------------------------
// opportunities namespace
// ----------------------------------------------------------------------

// _polyVariantFields is the column list shared by the variants and
// published tables — they hold polymorphic Opportunity records with
// identical shape (published is just the Verify-passing subset).
var _polyVariantFields = []iceberg.NestedField{
	req(1, "variant_id", iceberg.StringType{}),
	req(2, "source_id", iceberg.StringType{}),
	req(3, "external_id", iceberg.StringType{}),
	req(4, "hard_key", iceberg.StringType{}),
	req(5, "kind", iceberg.StringType{}),
	req(6, "stage", iceberg.StringType{}),
	req(7, "title", iceberg.StringType{}),
	opt(8, "issuing_entity", iceberg.StringType{}),
	opt(9, "country", iceberg.StringType{}),
	opt(10, "region", iceberg.StringType{}),
	opt(11, "city", iceberg.StringType{}),
	opt(12, "lat", iceberg.Float64Type{}),
	opt(13, "lon", iceberg.Float64Type{}),
	opt(14, "remote", iceberg.BooleanType{}),
	opt(15, "geo_scope", iceberg.StringType{}),
	opt(16, "currency", iceberg.StringType{}),
	opt(17, "amount_min", iceberg.Float64Type{}),
	opt(18, "amount_max", iceberg.Float64Type{}),
	opt(19, "deadline", iceberg.TimestampType{}),
	opt(20, "categories", iceberg.StringType{}), // comma-joined
	opt(21, "attributes", iceberg.StringType{}), // JSON
	req(22, "scraped_at", iceberg.TimestampType{}),
}

// SchemaVariants is opportunities.variants.
func SchemaVariants() *iceberg.Schema {
	return iceberg.NewSchema(0, _polyVariantFields...)
}

// SchemaVariantsRejected is opportunities.variants_rejected.
func SchemaVariantsRejected() *iceberg.Schema {
	return iceberg.NewSchema(0,
		req(1, "variant_id", iceberg.StringType{}),
		req(2, "source_id", iceberg.StringType{}),
		req(3, "kind", iceberg.StringType{}),
		req(4, "title", iceberg.StringType{}),
		req(5, "reasons", iceberg.StringType{}), // JSON array
		req(6, "rejected_at", iceberg.TimestampType{}),
	)
}

// SchemaEmbeddings is opportunities.embeddings.
func SchemaEmbeddings() *iceberg.Schema {
	return iceberg.NewSchema(0,
		req(1, "variant_id", iceberg.StringType{}),
		req(2, "kind", iceberg.StringType{}),
		iceberg.NestedField{ID: 3, Name: "vector", Type: f64List(100), Required: true},
		req(4, "embedded_at", iceberg.TimestampType{}),
	)
}

// SchemaPublished is opportunities.published — mirrors VARIANTS.
func SchemaPublished() *iceberg.Schema {
	return iceberg.NewSchema(0, _polyVariantFields...)
}

// SchemaCrawlPageCompleted is opportunities.crawl_page_completed.
func SchemaCrawlPageCompleted() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "request_id", iceberg.StringType{}),
		req(2, "source_id", iceberg.StringType{}),
		opt(3, "url", iceberg.StringType{}),
		opt(4, "http_status", iceberg.Int32Type{}),
		req(5, "jobs_found", iceberg.Int32Type{}),
		req(6, "jobs_emitted", iceberg.Int32Type{}),
		req(7, "jobs_rejected", iceberg.Int32Type{}),
		opt(8, "cursor", iceberg.StringType{}),
		opt(9, "error_code", iceberg.StringType{}),
		opt(10, "error_message", iceberg.StringType{}),
	}
	fields = append(fields, envelope(11)...)
	return iceberg.NewSchema(0, fields...)
}

// SchemaSourcesDiscovered is opportunities.sources_discovered.
func SchemaSourcesDiscovered() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "source_id", iceberg.StringType{}),
		req(2, "discovered_url", iceberg.StringType{}),
		opt(3, "name", iceberg.StringType{}),
		opt(4, "country", iceberg.StringType{}),
		opt(5, "type", iceberg.StringType{}),
	}
	fields = append(fields, envelope(6)...)
	return iceberg.NewSchema(0, fields...)
}

// ----------------------------------------------------------------------
// candidates namespace
// ----------------------------------------------------------------------

// SchemaCVUploaded is candidates.cv_uploaded.
func SchemaCVUploaded() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "candidate_id", iceberg.StringType{}),
		req(2, "cv_version", iceberg.Int32Type{}),
		req(3, "raw_archive_ref", iceberg.StringType{}),
		opt(4, "filename", iceberg.StringType{}),
		opt(5, "content_type", iceberg.StringType{}),
		opt(6, "size_bytes", iceberg.Int64Type{}),
		req(7, "extracted_text", iceberg.StringType{}),
	}
	fields = append(fields, envelope(8)...)
	return iceberg.NewSchema(0, fields...)
}

// SchemaCVExtracted is candidates.cv_extracted.
func SchemaCVExtracted() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "candidate_id", iceberg.StringType{}),
		req(2, "cv_version", iceberg.Int32Type{}),
		opt(3, "name", iceberg.StringType{}),
		opt(4, "email", iceberg.StringType{}),
		opt(5, "phone", iceberg.StringType{}),
		opt(6, "location", iceberg.StringType{}),
		opt(7, "current_title", iceberg.StringType{}),
		opt(8, "bio", iceberg.StringType{}),
		opt(9, "seniority", iceberg.StringType{}),
		opt(10, "years_experience", iceberg.Int32Type{}),
		opt(11, "primary_industry", iceberg.StringType{}),
		{ID: 12, Name: "strong_skills", Type: strList(200)},
		{ID: 13, Name: "working_skills", Type: strList(201)},
		{ID: 14, Name: "tools_frameworks", Type: strList(202)},
		{ID: 15, Name: "certifications", Type: strList(203)},
		{ID: 16, Name: "preferred_roles", Type: strList(204)},
		{ID: 17, Name: "languages", Type: strList(205)},
		opt(18, "education", iceberg.StringType{}),
		{ID: 19, Name: "preferred_locations", Type: strList(206)},
		opt(20, "remote_preference", iceberg.StringType{}),
		opt(21, "salary_min", iceberg.Int32Type{}),
		opt(22, "salary_max", iceberg.Int32Type{}),
		opt(23, "currency", iceberg.StringType{}),
		req(24, "score_ats", iceberg.Int32Type{}),
		req(25, "score_keywords", iceberg.Int32Type{}),
		req(26, "score_impact", iceberg.Int32Type{}),
		req(27, "score_role_fit", iceberg.Int32Type{}),
		req(28, "score_clarity", iceberg.Int32Type{}),
		req(29, "score_overall", iceberg.Int32Type{}),
		opt(30, "model_version_extract", iceberg.StringType{}),
		opt(31, "model_version_score", iceberg.StringType{}),
	}
	fields = append(fields, envelope(32)...)
	return iceberg.NewSchema(0, fields...)
}

// _cvFixStruct mirrors the StructType used by candidates.cv_improved.
var _cvFixStruct = &iceberg.StructType{
	FieldList: []iceberg.NestedField{
		req(300, "fix_id", iceberg.StringType{}),
		opt(301, "title", iceberg.StringType{}),
		opt(302, "impact", iceberg.StringType{}),
		opt(303, "category", iceberg.StringType{}),
		opt(304, "why", iceberg.StringType{}),
		req(305, "auto_applicable", iceberg.BooleanType{}),
		opt(306, "rewrite", iceberg.StringType{}),
	},
}

// SchemaCVImproved is candidates.cv_improved.
func SchemaCVImproved() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "candidate_id", iceberg.StringType{}),
		req(2, "cv_version", iceberg.Int32Type{}),
		{
			ID:       3,
			Name:     "fixes",
			Required: true,
			Type: &iceberg.ListType{
				ElementID:       307,
				Element:         _cvFixStruct,
				ElementRequired: false,
			},
		},
		opt(4, "model_version", iceberg.StringType{}),
	}
	fields = append(fields, envelope(5)...)
	return iceberg.NewSchema(0, fields...)
}

// SchemaPreferences is candidates.preferences.
func SchemaPreferences() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "candidate_id", iceberg.StringType{}),
		opt(2, "opt_ins_json", iceberg.StringType{}),
		req(3, "updated_at", iceberg.TimestampTzType{}),
	}
	fields = append(fields, envelope(4)...)
	return iceberg.NewSchema(0, fields...)
}

// SchemaCandidateEmbeddings is candidates.embeddings.
func SchemaCandidateEmbeddings() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "candidate_id", iceberg.StringType{}),
		req(2, "cv_version", iceberg.Int32Type{}),
		{ID: 3, Name: "vector", Type: f32List(500), Required: true},
		opt(4, "model_version", iceberg.StringType{}),
	}
	fields = append(fields, envelope(5)...)
	return iceberg.NewSchema(0, fields...)
}

// _matchRowStruct mirrors the StructType used by candidates.matches_ready.
var _matchRowStruct = &iceberg.StructType{
	FieldList: []iceberg.NestedField{
		req(600, "canonical_id", iceberg.StringType{}),
		req(601, "score", iceberg.Float64Type{}),
		opt(602, "rerank_score", iceberg.Float64Type{}),
	},
}

// SchemaMatchesReady is candidates.matches_ready.
func SchemaMatchesReady() *iceberg.Schema {
	fields := []iceberg.NestedField{
		req(1, "candidate_id", iceberg.StringType{}),
		req(2, "match_batch_id", iceberg.StringType{}),
		{
			ID:       3,
			Name:     "matches",
			Required: true,
			Type: &iceberg.ListType{
				ElementID:       603,
				Element:         _matchRowStruct,
				ElementRequired: false,
			},
		},
	}
	fields = append(fields, envelope(4)...)
	return iceberg.NewSchema(0, fields...)
}

// ----------------------------------------------------------------------
// Bootstrap descriptor — pairs each AppendOnlyTables identifier with the
// corresponding schema so the bootstrap subcommand can iterate.
// ----------------------------------------------------------------------

// TableSchema describes one Iceberg table to be bootstrapped: its
// (namespace, name) identifier and the schema constructor that returns
// the canonical column layout.
type TableSchema struct {
	Identifier table.Identifier
	Schema     func() *iceberg.Schema
}

// AllTableSchemas returns every (identifier, schema-fn) pair the
// bootstrap subcommand creates. Order matches AppendOnlyTables; both
// lists have the same length and must stay in sync — TestSchemasMatchTables
// enforces that.
func AllTableSchemas() []TableSchema {
	return []TableSchema{
		{Identifier: table.Identifier{"opportunities", "variants"}, Schema: SchemaVariants},
		{Identifier: table.Identifier{"opportunities", "variants_rejected"}, Schema: SchemaVariantsRejected},
		{Identifier: table.Identifier{"opportunities", "embeddings"}, Schema: SchemaEmbeddings},
		{Identifier: table.Identifier{"opportunities", "published"}, Schema: SchemaPublished},
		{Identifier: table.Identifier{"opportunities", "crawl_page_completed"}, Schema: SchemaCrawlPageCompleted},
		{Identifier: table.Identifier{"opportunities", "sources_discovered"}, Schema: SchemaSourcesDiscovered},
		{Identifier: table.Identifier{"candidates", "cv_uploaded"}, Schema: SchemaCVUploaded},
		{Identifier: table.Identifier{"candidates", "cv_extracted"}, Schema: SchemaCVExtracted},
		{Identifier: table.Identifier{"candidates", "cv_improved"}, Schema: SchemaCVImproved},
		{Identifier: table.Identifier{"candidates", "preferences"}, Schema: SchemaPreferences},
		{Identifier: table.Identifier{"candidates", "embeddings"}, Schema: SchemaCandidateEmbeddings},
		{Identifier: table.Identifier{"candidates", "matches_ready"}, Schema: SchemaMatchesReady},
	}
}

// BootstrapNamespaces lists the namespaces the bootstrap subcommand
// creates. Derived from AllTableSchemas.
func BootstrapNamespaces() []table.Identifier {
	seen := map[string]struct{}{}
	out := []table.Identifier{}
	for _, ts := range AllTableSchemas() {
		ns := ts.Identifier[0]
		if _, ok := seen[ns]; ok {
			continue
		}
		seen[ns] = struct{}{}
		out = append(out, table.Identifier{ns})
	}
	return out
}
