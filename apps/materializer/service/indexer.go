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

// replaceOne issues a single /replace against idx_opportunities_rt. The row
// id is a stable hash of opportunity_id — Manticore requires a bigint
// pk and keying on hash(opportunity_id) gives us idempotent upsert.
//
// TODO(opportunity-generification): Phase 3.3 will rewrite this against
// the new idx_opportunities_rt schema (kind + Attributes-driven facets).
// For now we map the universal envelope fields and pull a couple of
// well-known string Attributes through so the materializer compiles.
func (i *Indexer) replaceOne(ctx context.Context, r eventsv1.CanonicalUpsertedV1) error {
	desc, _ := r.Attributes["description"].(string)
	location, _ := r.Attributes["location_text"].(string)
	lang, _ := r.Attributes["language"].(string)
	remote, _ := r.Attributes["remote_type"].(string)
	employment, _ := r.Attributes["employment_type"].(string)
	seniority, _ := r.Attributes["seniority"].(string)
	category := ""
	if len(r.Categories) > 0 {
		category = r.Categories[0]
	}

	doc := map[string]any{
		"canonical_id":    r.OpportunityID,
		"slug":            r.Slug,
		"kind":            r.Kind,
		"title":           r.Title,
		"company":         r.IssuingEntity,
		"description":     desc,
		"location_text":   location,
		"category":        category,
		"country":         r.AnchorCountry,
		"language":        lang,
		"remote_type":     remote,
		"employment_type": employment,
		"seniority":       seniority,
		"salary_min":      uint64(r.AmountMin),
		"salary_max":      uint64(r.AmountMax),
		"currency":        r.Currency,
		"posted_at":       r.PostedAt.Unix(),
		"last_seen_at":    r.UpsertedAt.Unix(),
		"status":          "active",
	}
	return i.client.Replace(ctx, "idx_opportunities_rt", hashID(r.OpportunityID), doc)
}

// ApplyEmbeddingsParquet updates the `embedding` attribute on
// idx_opportunities_rt rows by canonical_id. If the row doesn't exist yet
// (embeddings can arrive slightly ahead of canonical for a fresh
// job), Manticore's /update returns updated=0 and the call is
// a no-op — the next canonical event will land the base row, and
// the next embedding event for that canonical will re-apply.
func (i *Indexer) ApplyEmbeddingsParquet(ctx context.Context, body []byte) (int, error) {
	rows, err := eventlog.ReadParquet[eventsv1.EmbeddingV1](body)
	if err != nil {
		return 0, fmt.Errorf("indexer: decode embeddings parquet: %w", err)
	}
	n := 0
	for _, r := range rows {
		id := hashID(r.OpportunityID)
		doc := map[string]any{
			"embedding":       r.Vector,
			"embedding_model": r.ModelVersion,
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
