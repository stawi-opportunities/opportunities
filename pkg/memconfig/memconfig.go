// Package memconfig provides cgroup-aware memory budget allocation.
//
// Every resource-sensitive subsystem calls NewBudget at startup to declare
// which share of pod memory it owns. The underlying limit is re-read from the
// cgroup hierarchy every 30 s so the system adapts when a pod is vertically
// scaled in-place.
//
// Priority order for determining the total available memory:
//  1. /sys/fs/cgroup/memory.max          (cgroup v2)
//  2. /sys/fs/cgroup/memory/memory.limit_in_bytes  (cgroup v1)
//  3. GOMEMLIMIT environment variable (bytes)
//  4. runtime.MemStats.Sys              (Go runtime report)
//  5. 512 MiB hardcoded default         (local dev / unknown)
package memconfig

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	cgroupV2File = "/sys/fs/cgroup/memory.max"
	cgroupV1File = "/sys/fs/cgroup/memory/memory.limit_in_bytes"

	defaultBytes   = 512 * 1024 * 1024 // 512 MiB — local dev fallback
	refreshEvery   = 30 * time.Second
)

// global cache so all Budget instances share one refresh goroutine.
var (
	globalOnce    sync.Once
	globalBytes   atomic.Int64 // updated by background refresher
)

// startRefresher initialises the background refresh goroutine once.
func startRefresher() {
	globalOnce.Do(func() {
		globalBytes.Store(readAvailableBytes())
		go func() {
			t := time.NewTicker(refreshEvery)
			defer t.Stop()
			for range t.C {
				globalBytes.Store(readAvailableBytes())
			}
		}()
	})
}

// AvailableBytes returns this pod's current memory limit, refreshed every 30 s.
func AvailableBytes() int64 {
	startRefresher()
	return globalBytes.Load()
}

// readAvailableBytes reads the limit from cgroup files, env, or runtime.
func readAvailableBytes() int64 {
	// 1. cgroup v2
	if b, ok := readCgroupV2(); ok {
		return b
	}
	// 2. cgroup v1
	if b, ok := readCgroupV1(); ok {
		return b
	}
	// 3. GOMEMLIMIT env var (the Go runtime also reads this, but we honour it
	//    so operators have a single knob that works even in test environments).
	if s := os.Getenv("GOMEMLIMIT"); s != "" {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && v > 0 {
			return v
		}
	}
	// 4. runtime.MemStats.Sys — total memory obtained from OS.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.Sys > 0 {
		return int64(ms.Sys)
	}
	// 5. hardcoded fallback
	return defaultBytes
}

// readCgroupV2 reads /sys/fs/cgroup/memory.max.
// Returns (bytes, true) on success; ("max" means unlimited → returns false).
func readCgroupV2() (int64, bool) {
	data, err := os.ReadFile(cgroupV2File)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	if s == "max" {
		return 0, false // unlimited — fall through
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// readCgroupV1 reads /sys/fs/cgroup/memory/memory.limit_in_bytes.
// A value of math.MaxInt64 (or ≥ 2^62) means unlimited.
func readCgroupV1() (int64, bool) {
	data, err := os.ReadFile(cgroupV1File)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	// v1 uses 9223372036854771712 (near MaxInt64) to mean "unlimited"
	if v > math.MaxInt64/2 {
		return 0, false
	}
	return v, true
}

// Budget represents a memory allocation for one subsystem. Safe for
// concurrent use; the underlying limit refreshes in the background.
type Budget struct {
	Name     string
	SharePct int
}

// NewBudget returns a Budget tied to the given share (1–100%) of pod memory.
// It also ensures the background refresher is running.
func NewBudget(name string, sharePct int) *Budget {
	startRefresher()
	if sharePct < 1 {
		sharePct = 1
	}
	if sharePct > 100 {
		sharePct = 100
	}
	return &Budget{Name: name, SharePct: sharePct}
}

// Bytes returns the current absolute byte budget for this share.
func (b *Budget) Bytes() int64 {
	total := AvailableBytes()
	return total * int64(b.SharePct) / 100
}

// BatchSizeFor returns how many records of perRecordBytes fit in Bytes()/2,
// providing a 2× headroom buffer. Minimum 1.
func (b *Budget) BatchSizeFor(perRecordBytes int) int {
	return budgetBatchSizeFor(b.Bytes(), perRecordBytes)
}

// budgetBatchSizeFor is the pure arithmetic helper used by BatchSizeFor and tests.
func budgetBatchSizeFor(budgetBytes int64, perRecordBytes int) int {
	if perRecordBytes <= 0 {
		return 1
	}
	half := budgetBytes / 2
	n := half / int64(perRecordBytes)
	if n < 1 {
		return 1
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(n)
}
