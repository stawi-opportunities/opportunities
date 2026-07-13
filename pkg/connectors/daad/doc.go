// Package daad covers the DAAD scholarship pilot — the first non-job
// kind validated end-to-end against the opportunity-generification
// pipeline.
//
// Crawl for the DAAD seed uses structured extract (schema.org JSON-LD
// via the generic_html / structured connector, or a recipe when one is
// active). The seed JSON (seeds/scholarships-daad.json) registers the
// source with kinds:[scholarship] so Verify enforces the scholarship
// kind contract (deadline, field_of_study).
//
// This package only carries the integration test that proves the
// extract → verify boundary on a representative DAAD detail page; it
// does not register a new connector type. Adding scholarship-specific
// crawl logic later is a non-breaking change — drop a Connector here
// implementing connectors.Connector and register it from
// apps/crawler/service/setup.go.
package daad
