package eventlog

import (
	"bytes"
	"fmt"
	"io"

	"github.com/parquet-go/parquet-go"
)

// WriteParquet serializes rows of type T to Parquet using struct tags
// and returns the bytes. Zero rows returns (nil, nil) so callers can
// skip uploading empty batches.
//
// Uses parquet-go's generic writer which derives the schema from
// `parquet:"…"` struct tags. All tags must match between the type's
// Go struct (pkg/events/v1) and whatever reads the Parquet downstream
// (Phase 2 materializer, analytics queries).
func WriteParquet[T any](rows []T) ([]byte, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[T](&buf)
	if _, err := w.Write(rows); err != nil {
		return nil, fmt.Errorf("eventlog: parquet write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("eventlog: parquet close: %w", err)
	}
	return buf.Bytes(), nil
}

// ReadParquet is the test-only inverse. Generic over T so tests can
// assert on typed rows. Consumed by writer_test.go and later by the
// Phase 2 materializer tests.
func ReadParquet[T any](body []byte) ([]T, error) {
	r := parquet.NewGenericReader[T](bytes.NewReader(body))
	defer func() { _ = r.Close() }()

	out := make([]T, r.NumRows())
	if len(out) == 0 {
		return nil, nil
	}
	if _, err := r.Read(out); err != nil && err != io.EOF {
		return nil, fmt.Errorf("eventlog: parquet read: %w", err)
	}
	return out, nil
}
