package icebergclient

// AppendOnlyTables is the authoritative list of Iceberg tables that
// the writer persists to — the tables subject to snapshot expiry,
// compaction, and the materializer's snapshot-lag gauge.
//
// Lives in pkg/icebergclient rather than apps/writer/service so every
// service (materializer, worker, api, candidates) can read it without
// a cross-app import — the per-app Dockerfiles only ship their own
// apps/<name>/ directory + pkg/, so keeping shared static data in
// pkg/ is a hard requirement for clean builds.
//
// If you add a new writer-persisted table, update THIS list and
// expire/compact maintenance picks it up on the next run. The
// telemetry observable-gauge callbacks also iterate this list.
// AppendOnlyTables lists the 12 Iceberg tables that the writer persists to
// and that snapshot expiry, compaction, and maintenance jobs operate on.
//
// Dropped from Iceberg (body now lives in R2 slug-direct JSON):
//   - jobs.canonicals      → s3://opportunities-content/jobs/<slug>.json
//   - jobs.translations    → s3://opportunities-content/jobs/<slug>/<lang>.json
//   - jobs.canonicals_expired → Frame event only; materializer subscribes directly
var AppendOnlyTables = [][]string{
	{"opportunities", "variants"},
	{"opportunities", "variants_rejected"},
	{"opportunities", "embeddings"},
	{"opportunities", "published"},
	{"opportunities", "crawl_page_completed"},
	{"opportunities", "sources_discovered"},
	{"candidates", "cv_uploaded"},
	{"candidates", "cv_extracted"},
	{"candidates", "cv_improved"},
	{"candidates", "preferences"},
	{"candidates", "embeddings"},
	{"candidates", "matches_ready"},
}
