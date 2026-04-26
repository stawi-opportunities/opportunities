package service

import (
	"context"
	"fmt"
	"hash/fnv"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/eventlog"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

// Indexer converts Parquet-decoded event rows into Manticore writes
// via the HTTP JSON API. Each public method corresponds to one
// collection (canonicals, embeddings). Methods are idempotent:
// using the same canonical_id → hashID as the Manticore row id
// makes /replace act as upsert.
type Indexer struct {
	client *searchindex.Client
}

// NewIndexer wraps a Manticore client.
func NewIndexer(c *searchindex.Client) *Indexer { return &Indexer{client: c} }

// ApplyCanonicalsParquet decodes a Parquet body of CanonicalUpsertedV1
// rows and issues one /replace per row.
func (i *Indexer) ApplyCanonicalsParquet(ctx context.Context, body []byte) (int, error) {
	rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
	if err != nil {
		return 0, fmt.Errorf("indexer: decode canonicals parquet: %w", err)
	}
	n := 0
	for _, r := range rows {
		if err := i.replaceOne(ctx, r); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// replaceOne issues a single /replace against idx_opportunities_rt. The
// row id is a stable hash of opportunity_id — Manticore requires a
// bigint pk, and keying on hash(opportunity_id) gives idempotent upsert.
func (i *Indexer) replaceOne(ctx context.Context, r eventsv1.CanonicalUpsertedV1) error {
	doc := buildDocFromCanonical(r)
	return i.client.Replace(ctx, "idx_opportunities_rt", hashID(r.OpportunityID), doc)
}

// ApplyEmbeddingsParquet updates the `embedding` attribute on
// idx_opportunities_rt rows by canonical_id. If the row doesn't exist
// yet (embeddings can arrive slightly ahead of canonical for a fresh
// opportunity), Manticore's /update returns updated=0 and the call is
// a no-op — the next canonical event will land the base row, and the
// next embedding event for that canonical will re-apply.
func (i *Indexer) ApplyEmbeddingsParquet(ctx context.Context, body []byte) (int, error) {
	rows, err := eventlog.ReadParquet[eventsv1.EmbeddingV1](body)
	if err != nil {
		return 0, fmt.Errorf("indexer: decode embeddings parquet: %w", err)
	}
	n := 0
	for _, r := range rows {
		id := hashID(r.OpportunityID)
		doc := map[string]any{
			"embedding": r.Vector,
		}
		if err := i.client.Update(ctx, "idx_opportunities_rt", id, doc); err != nil {
			return n, fmt.Errorf("indexer: update embedding: %w", err)
		}
		n++
	}
	return n, nil
}

// hashID maps a canonical_id (xid) to a stable bigint for Manticore's
// primary key.
func hashID(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// hashCategory maps a category slug ("Programming", "STEM") to a
// deterministic int64 for Manticore's multi64 categories column.
// The same string always maps to the same id, which makes filtering
// and faceting consistent across runs.
func hashCategory(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF) // clamp to positive
}

// categoryIDs converts a slice of category slugs into the int64 ids
// Manticore stores in the multi64 column.
func categoryIDs(cats []string) []int64 {
	if len(cats) == 0 {
		return nil
	}
	out := make([]int64, 0, len(cats))
	for _, c := range cats {
		if c == "" {
			continue
		}
		out = append(out, hashCategory(c))
	}
	return out
}

// sparseColsForKind returns the per-kind sparse-facet column names and
// values to fold into the universal Manticore document. Returns parallel
// slices so callers can splice them straight into a doc map.
func sparseColsForKind(kind string, attrs map[string]any) (cols []string, vals []any) {
	switch kind {
	case "job":
		cols = []string{"employment_type", "seniority"}
		vals = []any{attrString(attrs, "employment_type"), attrString(attrs, "seniority")}
	case "scholarship":
		cols = []string{"field_of_study", "degree_level"}
		vals = []any{attrString(attrs, "field_of_study"), attrString(attrs, "degree_level")}
	case "tender":
		cols = []string{"procurement_domain"}
		vals = []any{attrString(attrs, "procurement_domain")}
	case "deal":
		cols = []string{"discount_percent"}
		vals = []any{attrFloat(attrs, "discount_percent")}
	case "funding":
		cols = []string{"funding_focus"}
		vals = []any{attrString(attrs, "funding_focus")}
	}
	return
}

// attrString reads Attributes[key] as a string, or "" if missing/wrong type.
// Mirrors domain.ExternalOpportunity.AttrString but operates on a raw map
// (the materializer consumes CanonicalUpsertedV1.Attributes, not domain
// objects).
func attrString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// attrFloat reads Attributes[key] as a float64, or 0 if missing/wrong type.
func attrFloat(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
