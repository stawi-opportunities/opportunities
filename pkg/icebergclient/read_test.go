package icebergclient

import (
	"context"
	"os"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// TestMapArrowBatch_FlattensRowsAndRespectsCap is a unit test for the
// pure helper that converts an arrow.RecordBatch into row-shaped maps.
// Runs in -short mode; no Iceberg dependency.
func TestMapArrowBatch_FlattensRowsAndRespectsCap(t *testing.T) {
	mem := memory.NewGoAllocator()

	schema := arrow.NewSchema([]arrow.Field{
		{Name: "source_id", Type: arrow.BinaryTypes.String},
		{Name: "count", Type: arrow.PrimitiveTypes.Int64},
	}, nil)

	sb := array.NewStringBuilder(mem)
	sb.AppendValues([]string{"src-a", "src-b", "src-c"}, nil)
	srcCol := sb.NewArray()
	defer srcCol.Release()

	ib := array.NewInt64Builder(mem)
	ib.AppendValues([]int64{1, 2, 3}, nil)
	cntCol := ib.NewArray()
	defer cntCol.Release()

	rec := array.NewRecordBatch(schema, []arrow.Array{srcCol, cntCol}, 3)
	defer rec.Release()

	t.Run("returns all rows when cap >= numRows", func(t *testing.T) {
		rows := mapArrowBatch(rec, 10)
		if len(rows) != 3 {
			t.Fatalf("len(rows) = %d, want 3", len(rows))
		}
		if rows[0]["source_id"] != "src-a" {
			t.Errorf("rows[0].source_id = %v, want 'src-a'", rows[0]["source_id"])
		}
		if rows[2]["count"] != int64(3) {
			t.Errorf("rows[2].count = %v, want 3", rows[2]["count"])
		}
	})

	t.Run("caps at cap when cap < numRows", func(t *testing.T) {
		rows := mapArrowBatch(rec, 2)
		if len(rows) != 2 {
			t.Fatalf("len(rows) = %d, want 2 (capped)", len(rows))
		}
		if rows[1]["source_id"] != "src-b" {
			t.Errorf("rows[1].source_id = %v, want 'src-b'", rows[1]["source_id"])
		}
	})

	t.Run("returns nil for cap <= 0", func(t *testing.T) {
		if rows := mapArrowBatch(rec, 0); rows != nil {
			t.Errorf("rows = %v, want nil", rows)
		}
		if rows := mapArrowBatch(rec, -5); rows != nil {
			t.Errorf("rows = %v, want nil", rows)
		}
	})
}

// TestNewCatalogFromEnv_NoURIErrors confirms the env-driven constructor
// surfaces a meaningful error when ICEBERG_CATALOG_URI is unset. The
// admin path catches this and disables historic queries gracefully.
func TestNewCatalogFromEnv_NoURIErrors(t *testing.T) {
	t.Setenv("ICEBERG_CATALOG_URI", "")
	_, err := NewCatalogFromEnv(context.Background())
	if err == nil {
		t.Fatal("NewCatalogFromEnv returned nil error with no URI; want error")
	}
}

// TestReadRecent_VariantsRejected is an integration test against the
// real R2 catalog. Skipped without R2_ACCOUNT_ID. Build tag keeps it
// out of the default test suite.
//
// Build with: go test -tags=integration ./pkg/icebergclient/...
func TestReadRecent_VariantsRejected(t *testing.T) {
	if os.Getenv("R2_ACCOUNT_ID") == "" || os.Getenv("ICEBERG_CATALOG_URI") == "" {
		t.Skip("R2_ACCOUNT_ID or ICEBERG_CATALOG_URI not set; integration test skipped")
	}
	if testing.Short() {
		t.Skip("short mode; skipping integration test")
	}
	ctx := context.Background()
	cat, err := NewCatalogFromEnv(ctx)
	if err != nil {
		t.Fatalf("NewCatalogFromEnv: %v", err)
	}
	rows, err := cat.ReadRecent(ctx, "opportunities", "variants_rejected",
		[]string{"variant_id", "source_id", "kind", "rejected_at"}, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	t.Logf("read %d rows", len(rows))
}
