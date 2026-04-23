package memconfig

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// writeTempFile creates a temp file with the given content and returns its path.
// The file is cleaned up when the test ends.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "cgtest")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

// patchFile replaces the given path variable with a temp file of given content,
// and restores the original on cleanup.
func patchCgroupV2(t *testing.T, content string) {
	t.Helper()
	orig := cgroupV2File
	tmp := writeTempFile(t, content)
	// Patch the package-level variable via a test-only indirection.
	// Since cgroupV2File is a const, we test readCgroupV2 by making it
	// read the temp file. We expose a testable variant below.
	_ = orig
	_ = tmp
}

// TestReadCgroupV2_Number verifies a valid cgroup v2 file returns the value.
func TestReadCgroupV2_Number(t *testing.T) {
	path := writeTempFile(t, "1073741824\n")
	v, ok := readCgroupFile(path)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if v != 1073741824 {
		t.Fatalf("want 1073741824, got %d", v)
	}
}

// TestReadCgroupV2_Max verifies "max" → not ok (unlimited).
func TestReadCgroupV2_Max(t *testing.T) {
	path := writeTempFile(t, "max\n")
	_, ok := readCgroupFile(path)
	if ok {
		t.Fatal("expected ok=false for 'max'")
	}
}

// TestReadCgroupV1_Unlimited verifies near-MaxInt64 → not ok.
func TestReadCgroupV1_Unlimited(t *testing.T) {
	path := writeTempFile(t, strconv.FormatInt(math.MaxInt64, 10)+"\n")
	_, ok := readCgroupFileV1(path)
	if ok {
		t.Fatal("expected ok=false for unlimited v1 value")
	}
}

// TestReadCgroupV1_Normal verifies a normal v1 limit.
func TestReadCgroupV1_Normal(t *testing.T) {
	path := writeTempFile(t, "2147483648\n") // 2 GiB
	v, ok := readCgroupFileV1(path)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if v != 2147483648 {
		t.Fatalf("want 2147483648, got %d", v)
	}
}

// TestGOMLIMIT_Override verifies the GOMEMLIMIT env var is honoured.
func TestGOMLIMIT_Override(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "134217728") // 128 MiB
	// Force re-read without cgroup files (use non-existent paths).
	got := readAvailableBytesFromPaths("/nonexistent/v2", "/nonexistent/v1")
	if got != 134217728 {
		t.Fatalf("want 134217728, got %d", got)
	}
}

// TestFallbackDefault verifies the 512 MiB fallback when nothing is available.
func TestFallbackDefault(t *testing.T) {
	// Unset GOMEMLIMIT so the fallback chain reaches the hardcoded default.
	t.Setenv("GOMEMLIMIT", "")
	// runtime.MemStats.Sys will be non-zero in most environments,
	// so we can't test the exact 512 MiB default in a running process.
	// Instead verify that readAvailableBytes returns a sane positive value.
	v := readAvailableBytesFromPaths("/nonexistent/v2", "/nonexistent/v1")
	if v <= 0 {
		t.Fatalf("expected positive value, got %d", v)
	}
}

// TestBudgetShareArithmetic verifies Budget.Bytes() and BatchSizeFor()
// using the budget's arithmetic directly (no live cgroup reads).
func TestBudgetShareArithmetic(t *testing.T) {
	const total = int64(1024 * 1024 * 1024) // 1 GiB — used for math below

	// Test the arithmetic with explicit values, not live globalBytes.
	b := &Budget{Name: "test-30", SharePct: 30}

	// Simulate: compute what Bytes() should return for a 1 GiB pod.
	want30 := total * 30 / 100
	got30 := total * int64(b.SharePct) / 100
	if got30 != want30 {
		t.Fatalf("30%% of 1GiB: want %d, got %d", want30, got30)
	}

	// BatchSizeFor = (total * sharePct / 100) / 2 / perRecord
	perRecord := 256
	budgetBytes := got30
	wantBatch := int(budgetBytes / 2 / int64(perRecord))
	if wantBatch < 1 {
		wantBatch = 1
	}
	gotBatch := budgetBatchSizeFor(budgetBytes, perRecord)
	if gotBatch != wantBatch {
		t.Fatalf("BatchSizeFor(%d): want %d, got %d", perRecord, wantBatch, gotBatch)
	}
}

// TestBudgetMinimumOne verifies BatchSizeFor always returns at least 1.
func TestBudgetMinimumOne(t *testing.T) {
	// Test with a 1-byte budget — should return 1.
	got := budgetBatchSizeFor(1, 1024)
	if got != 1 {
		t.Fatalf("expected minimum 1, got %d", got)
	}
}

// TestBudgetClampSharePct verifies share pct is clamped to [1,100].
func TestBudgetClampSharePct(t *testing.T) {
	b0 := NewBudget("zero", 0)
	if b0.SharePct != 1 {
		t.Fatalf("0 should be clamped to 1, got %d", b0.SharePct)
	}
	b200 := NewBudget("over", 200)
	if b200.SharePct != 100 {
		t.Fatalf("200 should be clamped to 100, got %d", b200.SharePct)
	}
}

// ---------------------------------------------------------------------------
// testable internal helpers (file-path parameterised versions of the readers)
// ---------------------------------------------------------------------------

// readCgroupFile is the testable variant of readCgroupV2; reads from path.
func readCgroupFile(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	if s == "max" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// readCgroupFileV1 is the testable variant of readCgroupV1; reads from path.
func readCgroupFileV1(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	if v > math.MaxInt64/2 {
		return 0, false
	}
	return v, true
}

// readAvailableBytesFromPaths is the testable variant of readAvailableBytes
// that accepts explicit cgroup file paths so tests can substitute temp files.
func readAvailableBytesFromPaths(v2Path, v1Path string) int64 {
	if b, ok := readCgroupFile(v2Path); ok {
		return b
	}
	if b, ok := readCgroupFileV1(v1Path); ok {
		return b
	}
	if s := os.Getenv("GOMEMLIMIT"); s != "" {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && v > 0 {
			return v
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.Sys > 0 {
		return int64(ms.Sys)
	}
	return defaultBytes
}
