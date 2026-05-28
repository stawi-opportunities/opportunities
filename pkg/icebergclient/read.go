// read.go — admin-side read path for historic Iceberg data.
//
// The writer is append-only and Postgres' pipeline_variants retains 7d;
// anything older has to be read from Iceberg. ReadRecent is the
// minimum-viable helper: given a (namespace, table, columns, limit) it
// pulls up to `limit` rows projected to those columns and returns them
// as []map[string]any so the admin handler can serialize to JSON.
//
// Why not stream? The admin trace endpoints aggregate a small window
// (a single source over a few days) and the projections are 3-5 cols.
// The buffered shape keeps the handlers simple — if a future caller
// needs streaming, add ReadRecentStream and migrate just that path.

package icebergclient

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
)

// Catalog wraps the iceberg-go catalog.Catalog interface with the small
// admin-side read helpers needed by the api's trace endpoints. Held by
// reference; safe for concurrent use across goroutines because the
// underlying catalog.Catalog is too.
type Catalog struct {
	cat  catalog.Catalog
	name string
}

// NewCatalog wraps an already-loaded catalog.Catalog. Used by tests
// that want to inject a fake; production callers prefer NewCatalogFromEnv.
func NewCatalog(cat catalog.Catalog, name string) *Catalog {
	return &Catalog{cat: cat, name: name}
}

// NewCatalogFromEnv reads ICEBERG_CATALOG_URI / NAME / WAREHOUSE /
// TOKEN from the process environment (same names the writer uses) and
// returns a ready Catalog wrapper.
//
// Returns an error when ICEBERG_CATALOG_URI is unset — the caller
// decides whether that's fatal (writer) or a soft "historic queries
// disabled" warning (api).
func NewCatalogFromEnv(ctx context.Context) (*Catalog, error) {
	uri := os.Getenv("ICEBERG_CATALOG_URI")
	if uri == "" {
		return nil, errors.New("ICEBERG_CATALOG_URI not set")
	}
	name := os.Getenv("ICEBERG_CATALOG_NAME")
	if name == "" {
		name = "stawi"
	}
	cfg := CatalogConfig{
		Name:       name,
		URI:        uri,
		Warehouse:  os.Getenv("ICEBERG_WAREHOUSE"),
		OAuthToken: os.Getenv("ICEBERG_CATALOG_TOKEN"),
	}
	cat, err := LoadCatalog(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("LoadCatalog: %w", err)
	}
	return &Catalog{cat: cat, name: name}, nil
}

// Underlying returns the wrapped catalog.Catalog so callers (e.g. the
// writer) that need the full interface can reach through. Admin code
// should use the typed helpers on *Catalog instead.
func (c *Catalog) Underlying() catalog.Catalog { return c.cat }

// ReadRecent returns up to `limit` rows from `namespace.tableName`
// projected to the requested columns. Used by admin trace endpoints
// that need historic data beyond Postgres' 7d retention.
//
// The result is []map[string]any so the caller can JSON-serialize
// directly. Column values are whatever Arrow's GetOneForMarshal
// returns — strings stay strings, ints stay ints, timestamps come
// back as arrow's marshal shape (typically RFC3339-style strings).
// Callers that need typed access should inspect after marshaling.
//
// Missing tables (catalog.ErrNoSuchTable) return an empty slice and
// nil error so fresh installs don't trip the admin endpoints.
func (c *Catalog) ReadRecent(
	ctx context.Context,
	namespace, tableName string,
	columns []string,
	limit int,
) ([]map[string]any, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	ident := table.Identifier{namespace, tableName}
	tbl, err := c.cat.LoadTable(ctx, ident)
	if err != nil {
		if errors.Is(err, catalog.ErrNoSuchTable) {
			return []map[string]any{}, nil
		}
		return nil, fmt.Errorf("LoadTable %v: %w", ident, err)
	}

	opts := []table.ScanOption{table.WithLimit(int64(limit))}
	if len(columns) > 0 {
		opts = append(opts, table.WithSelectedFields(columns...))
	}
	scan := tbl.Scan(opts...)

	// iceberg-go v0.5.0 returns the schema up-front (so callers can
	// know the projection on empty reads) and an iterator over
	// (RecordBatch, error). Each batch is multi-row; we flatten to
	// rows with mapArrowBatch.
	_, batches, err := scan.ToArrowRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan %v: %w", ident, err)
	}

	rows := make([]map[string]any, 0, limit)
	for batch, iterErr := range batches {
		if iterErr != nil {
			return nil, fmt.Errorf("scan iter %v: %w", ident, iterErr)
		}
		if batch == nil {
			continue
		}
		rows = append(rows, mapArrowBatch(batch, limit-len(rows))...)
		batch.Release()
		if len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

// mapArrowBatch flattens an arrow.RecordBatch to []map[string]any,
// capping at `cap` rows. Pure function — unit-testable without R2.
func mapArrowBatch(batch arrow.RecordBatch, cap int) []map[string]any {
	if cap <= 0 {
		return nil
	}
	schema := batch.Schema()
	nrows := int(batch.NumRows())
	ncols := int(batch.NumCols())
	if nrows > cap {
		nrows = cap
	}
	out := make([]map[string]any, 0, nrows)
	for i := 0; i < nrows; i++ {
		row := make(map[string]any, ncols)
		for j := 0; j < ncols; j++ {
			row[schema.Field(j).Name] = batch.Column(j).GetOneForMarshal(i)
		}
		out = append(out, row)
	}
	return out
}
