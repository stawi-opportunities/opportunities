package service

// manticore_bulk.go — buffered bulk upsert to Manticore's /bulk endpoint.
//
// BulkUpserter accumulates replace ops in memory and flushes them as
// NDJSON to /bulk once the buffer reaches maxRows or on an explicit
// Flush call. This reduces HTTP RTTs by roughly 100× vs per-row
// /replace calls.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

// BulkUpserter buffers Manticore replace operations and flushes them
// in batches to /bulk.
type BulkUpserter struct {
	client  *searchindex.Client
	index   string
	maxRows int
	buffer  []bulkOp
}

type bulkOp struct {
	ID  uint64
	Doc map[string]any
}

// NewBulkUpserter creates a BulkUpserter that flushes when the buffer
// hits maxRows. A maxRows ≤ 0 defaults to 500.
func NewBulkUpserter(c *searchindex.Client, index string, maxRows int) *BulkUpserter {
	if maxRows <= 0 {
		maxRows = 500
	}
	return &BulkUpserter{client: c, index: index, maxRows: maxRows}
}

// Add enqueues a single replace operation. Automatically flushes if
// the buffer is full.
func (b *BulkUpserter) Add(ctx context.Context, id uint64, doc map[string]any) error {
	b.buffer = append(b.buffer, bulkOp{ID: id, Doc: doc})
	if len(b.buffer) >= b.maxRows {
		return b.Flush(ctx)
	}
	return nil
}

// Flush sends all buffered operations to /bulk and resets the buffer.
// A no-op if the buffer is empty.
func (b *BulkUpserter) Flush(ctx context.Context) error {
	if len(b.buffer) == 0 {
		return nil
	}
	// Build NDJSON: one {"replace":{...}} JSON object per line.
	var ndjson bytes.Buffer
	for _, op := range b.buffer {
		line, err := json.Marshal(map[string]any{
			"replace": map[string]any{
				"index": b.index,
				"id":    op.ID,
				"doc":   op.Doc,
			},
		})
		if err != nil {
			return fmt.Errorf("manticore bulk: marshal op id=%d: %w", op.ID, err)
		}
		ndjson.Write(line)
		ndjson.WriteByte('\n')
	}
	if err := b.client.Bulk(ctx, ndjson.Bytes()); err != nil {
		return fmt.Errorf("manticore bulk flush: %w", err)
	}
	b.buffer = b.buffer[:0]
	return nil
}

// Len returns the number of buffered (unflushed) operations.
func (b *BulkUpserter) Len() int { return len(b.buffer) }
