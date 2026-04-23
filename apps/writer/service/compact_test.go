package service

import (
	"testing"
	"time"

	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/table"
)

// ---------------------------------------------------------------------------
// applyCompactDefaults
// ---------------------------------------------------------------------------

func TestCompactConfig_Defaults(t *testing.T) {
	cfg := CompactConfig{} // all zero
	got := applyCompactDefaults(cfg)

	if want := int64(134217728); got.TargetFileSize != want {
		t.Errorf("TargetFileSize default: got %d, want %d", got.TargetFileSize, want)
	}
	if want := int64(67108864); got.MinFileSize != want {
		t.Errorf("MinFileSize default: got %d, want %d", got.MinFileSize, want)
	}
	if want := 20; got.MaxInputPerCommit != want {
		t.Errorf("MaxInputPerCommit default: got %d, want %d", got.MaxInputPerCommit, want)
	}
	if want := 30 * time.Minute; got.PerTableTimeout != want {
		t.Errorf("PerTableTimeout default: got %v, want %v", got.PerTableTimeout, want)
	}
	if want := 4; got.Parallelism != want {
		t.Errorf("Parallelism default: got %d, want %d", got.Parallelism, want)
	}
}

func TestCompactConfig_ExplicitValues(t *testing.T) {
	cfg := CompactConfig{
		TargetFileSize:    256 * 1024 * 1024,
		MinFileSize:       128 * 1024 * 1024,
		MaxInputPerCommit: 10,
		PerTableTimeout:   15 * time.Minute,
		Parallelism:       8,
	}
	got := applyCompactDefaults(cfg)

	if got.TargetFileSize != cfg.TargetFileSize {
		t.Errorf("TargetFileSize must be preserved: got %d, want %d", got.TargetFileSize, cfg.TargetFileSize)
	}
	if got.MinFileSize != cfg.MinFileSize {
		t.Errorf("MinFileSize must be preserved: got %d, want %d", got.MinFileSize, cfg.MinFileSize)
	}
	if got.MaxInputPerCommit != cfg.MaxInputPerCommit {
		t.Errorf("MaxInputPerCommit must be preserved: got %d, want %d", got.MaxInputPerCommit, cfg.MaxInputPerCommit)
	}
	if got.PerTableTimeout != cfg.PerTableTimeout {
		t.Errorf("PerTableTimeout must be preserved: got %v, want %v", got.PerTableTimeout, cfg.PerTableTimeout)
	}
	if got.Parallelism != cfg.Parallelism {
		t.Errorf("Parallelism must be preserved: got %d, want %d", got.Parallelism, cfg.Parallelism)
	}
}

// ---------------------------------------------------------------------------
// partitionKey
// ---------------------------------------------------------------------------

// stubDataFile is a minimal iceberg.DataFile for testing partition grouping.
type stubDataFile struct {
	path      string
	sizeBytes int64
	partition map[int]any
}

func (s *stubDataFile) FilePath() string               { return s.path }
func (s *stubDataFile) FileFormat() iceberg.FileFormat { return iceberg.ParquetFile }
func (s *stubDataFile) Partition() map[int]any         { return s.partition }
func (s *stubDataFile) FileSizeBytes() int64           { return s.sizeBytes }
func (s *stubDataFile) Count() int64                   { return 0 }
func (s *stubDataFile) ContentType() iceberg.ManifestEntryContent {
	return iceberg.EntryContentData
}
func (s *stubDataFile) SpecID() int32                                      { return 0 }
func (s *stubDataFile) ColumnSizes() map[int]int64                         { return nil }
func (s *stubDataFile) ValueCounts() map[int]int64                         { return nil }
func (s *stubDataFile) NullValueCounts() map[int]int64                     { return nil }
func (s *stubDataFile) NaNValueCounts() map[int]int64                      { return nil }
func (s *stubDataFile) DistinctValueCounts() map[int]int64                 { return nil }
func (s *stubDataFile) LowerBoundValues() map[int][]byte                   { return nil }
func (s *stubDataFile) UpperBoundValues() map[int][]byte                   { return nil }
func (s *stubDataFile) KeyMetadata() []byte                                { return nil }
func (s *stubDataFile) SplitOffsets() []int64                              { return nil }
func (s *stubDataFile) EqualityFieldIDs() []int                            { return nil }
func (s *stubDataFile) SortOrderID() *int                                  { return nil }
func (s *stubDataFile) FirstRowID() *int64                                 { return nil }
func (s *stubDataFile) ContentOffset() *int64                              { return nil }
func (s *stubDataFile) ContentSizeInBytes() *int64                         { return nil }
func (s *stubDataFile) ReferencedDataFile() *string                        { return nil }

func TestPartitionKey_Deterministic(t *testing.T) {
	// Two files with the same partition values must produce the same key.
	f1 := &stubDataFile{path: "a", partition: map[int]any{1: 42, 2: "us"}}
	f2 := &stubDataFile{path: "b", partition: map[int]any{2: "us", 1: 42}}
	k1 := partitionKey(f1)
	k2 := partitionKey(f2)
	if k1 != k2 {
		t.Errorf("same partition values must produce same key; k1=%q k2=%q", k1, k2)
	}
}

func TestPartitionKey_DifferentPartitions(t *testing.T) {
	// Files in different partitions must produce different keys.
	f1 := &stubDataFile{path: "a", partition: map[int]any{1: 42}}
	f2 := &stubDataFile{path: "b", partition: map[int]any{1: 99}}
	k1 := partitionKey(f1)
	k2 := partitionKey(f2)
	if k1 == k2 {
		t.Errorf("different partition values must produce different keys; k1=%q k2=%q", k1, k2)
	}
}

func TestPartitionKey_Unpartitioned(t *testing.T) {
	f := &stubDataFile{path: "a", partition: nil}
	if got := partitionKey(f); got != "__unpartitioned__" {
		t.Errorf("unpartitioned file key: got %q, want %q", got, "__unpartitioned__")
	}
}

// ---------------------------------------------------------------------------
// groupByPartition
// ---------------------------------------------------------------------------

func TestGroupByPartition(t *testing.T) {
	tasks := []table.FileScanTask{
		{File: &stubDataFile{path: "a", partition: map[int]any{1: 1}}},
		{File: &stubDataFile{path: "b", partition: map[int]any{1: 1}}},
		{File: &stubDataFile{path: "c", partition: map[int]any{1: 2}}},
	}
	groups := groupByPartition(tasks)
	if len(groups) != 2 {
		t.Fatalf("expected 2 partition groups, got %d", len(groups))
	}
	for _, g := range groups {
		switch partitionKey(g[0].File) {
		case partitionKey(tasks[0].File):
			if len(g) != 2 {
				t.Errorf("partition 1 should have 2 files, got %d", len(g))
			}
		case partitionKey(tasks[2].File):
			if len(g) != 1 {
				t.Errorf("partition 2 should have 1 file, got %d", len(g))
			}
		default:
			t.Errorf("unexpected partition key")
		}
	}
}

// ---------------------------------------------------------------------------
// selectSmallFiles
// ---------------------------------------------------------------------------

func TestSelectSmallFiles_PicksSmallest(t *testing.T) {
	const minSize = 64 * 1024 * 1024
	tasks := []table.FileScanTask{
		{File: &stubDataFile{sizeBytes: 10 * 1024 * 1024}},  // small
		{File: &stubDataFile{sizeBytes: 5 * 1024 * 1024}},   // small (smallest)
		{File: &stubDataFile{sizeBytes: 128 * 1024 * 1024}}, // large — excluded
		{File: &stubDataFile{sizeBytes: 20 * 1024 * 1024}},  // small
	}
	got := selectSmallFiles(tasks, 5, minSize)
	if len(got) != 3 {
		t.Fatalf("expected 3 small files, got %d", len(got))
	}
	// Must be sorted ascending (smallest first).
	for i := 1; i < len(got); i++ {
		if got[i].File.FileSizeBytes() < got[i-1].File.FileSizeBytes() {
			t.Errorf("files not sorted ascending by size at index %d", i)
		}
	}
}

func TestSelectSmallFiles_CappedByN(t *testing.T) {
	const minSize = 64 * 1024 * 1024
	tasks := []table.FileScanTask{
		{File: &stubDataFile{sizeBytes: 1 * 1024 * 1024}},
		{File: &stubDataFile{sizeBytes: 2 * 1024 * 1024}},
		{File: &stubDataFile{sizeBytes: 3 * 1024 * 1024}},
		{File: &stubDataFile{sizeBytes: 4 * 1024 * 1024}},
	}
	got := selectSmallFiles(tasks, 2, minSize)
	if len(got) != 2 {
		t.Fatalf("expected 2 files (capped by n), got %d", len(got))
	}
}

func TestSelectSmallFiles_AllLarge(t *testing.T) {
	const minSize = 64 * 1024 * 1024
	tasks := []table.FileScanTask{
		{File: &stubDataFile{sizeBytes: 100 * 1024 * 1024}},
		{File: &stubDataFile{sizeBytes: 200 * 1024 * 1024}},
	}
	got := selectSmallFiles(tasks, 5, minSize)
	if len(got) != 0 {
		t.Fatalf("expected 0 small files when all are large, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// compactPartition — skip logic (no catalog required)
// ---------------------------------------------------------------------------

func TestCompactPartition_SingleFile_Skipped(t *testing.T) {
	tasks := []table.FileScanTask{
		{File: &stubDataFile{path: "a", sizeBytes: 1 * 1024 * 1024}},
	}
	// compactPartition with a nil table would panic at Overwrite, but the
	// single-file check fires before any table access.
	processed, skipReason, _, _, err := compactPartition(nil, nil, tasks,
		CompactConfig{MinFileSize: 64 * 1024 * 1024, MaxInputPerCommit: 20}, "p0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if processed {
		t.Error("single-file partition must not be processed")
	}
	if skipReason != "single_file" {
		t.Errorf("expected skip reason 'single_file', got %q", skipReason)
	}
}

func TestCompactPartition_AllLarge_Skipped(t *testing.T) {
	const minSize = int64(64 * 1024 * 1024)
	tasks := []table.FileScanTask{
		{File: &stubDataFile{path: "a", sizeBytes: 200 * 1024 * 1024}},
		{File: &stubDataFile{path: "b", sizeBytes: 150 * 1024 * 1024}},
	}
	processed, skipReason, _, _, err := compactPartition(nil, nil, tasks,
		CompactConfig{MinFileSize: minSize, MaxInputPerCommit: 20}, "p0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if processed {
		t.Error("all-large partition must not be processed")
	}
	if skipReason != "already_large" {
		t.Errorf("expected skip reason 'already_large', got %q", skipReason)
	}
}

// TestCompactPartition_SelectionNotEnough verifies that if fewer than 2 small
// files exist (e.g. only 1 file is small and there are large files too), the
// partition is also skipped.
func TestCompactPartition_SelectionNotEnough_Skipped(t *testing.T) {
	const minSize = int64(64 * 1024 * 1024)
	tasks := []table.FileScanTask{
		{File: &stubDataFile{path: "a", sizeBytes: 10 * 1024 * 1024}},  // small — only 1
		{File: &stubDataFile{path: "b", sizeBytes: 200 * 1024 * 1024}}, // large
		{File: &stubDataFile{path: "c", sizeBytes: 300 * 1024 * 1024}}, // large
	}
	processed, skipReason, _, _, err := compactPartition(nil, nil, tasks,
		CompactConfig{MinFileSize: minSize, MaxInputPerCommit: 20}, "p0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if processed {
		t.Error("partition with only 1 small file must not be processed")
	}
	if skipReason != "single_file" {
		t.Errorf("expected skip reason 'single_file', got %q", skipReason)
	}
}

// ---------------------------------------------------------------------------
// equalityForPartitionValue
// ---------------------------------------------------------------------------

func TestEqualityForPartitionValue_Types(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"int", 42},
		{"int32", int32(7)},
		{"int64", int64(999)},
		{"float32", float32(3.14)},
		{"float64", 3.14},
		{"string", "abc"},
		{"bool", true},
		{"bytes", []byte("hello")},
		{"unknown", struct{ X int }{X: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := equalityForPartitionValue("dt_day", tc.val)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if expr == nil {
				t.Error("expected non-nil expression")
			}
		})
	}
}
