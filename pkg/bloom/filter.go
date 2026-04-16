package bloom

import (
	"context"
	"fmt"
	"hash/crc32"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
)

const bitmapSize = 1 << 20 // 1M bits per source (~128KB, <1% FPR at 50K items)

// Filter provides fast duplicate detection using Valkey (Redis-compatible) bitmaps,
// with a database fallback when Valkey is unavailable.
type Filter struct {
	valkey *redis.Client
	db     func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewFilter creates a Filter. If valkeyAddr is non-empty, it attempts a ping;
// on failure (or empty addr) the filter runs in DB-only mode.
func NewFilter(valkeyAddr string, db func(ctx context.Context, readOnly bool) *gorm.DB) *Filter {
	f := &Filter{db: db}
	if valkeyAddr == "" {
		return f
	}
	client := redis.NewClient(&redis.Options{Addr: valkeyAddr})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return f
	}
	f.valkey = client
	return f
}

// bloomOffset returns the bit offset for hardKey within the per-source bitmap.
func bloomOffset(hardKey string) int64 {
	return int64(crc32.ChecksumIEEE([]byte(hardKey)) % bitmapSize)
}

// IsSeen reports whether hardKey has been seen for the given sourceID.
//  1. If Valkey is available: check the bit; a set bit means seen.
//  2. Otherwise (or when the bit is clear): query job_variants for a matching hard_key.
func IsSeen(ctx context.Context, f *Filter, sourceID int64, hardKey string) bool {
	if f.valkey != nil {
		key := fmt.Sprintf("bloom:seen:%d", sourceID)
		offset := bloomOffset(hardKey)
		bit, err := f.valkey.GetBit(ctx, key, offset).Result()
		if err == nil && bit == 1 {
			return true
		}
	}

	// DB fallback
	var count int64
	f.db(ctx, true).Model(&domain.JobVariant{}).
		Where("hard_key = ?", hardKey).
		Count(&count)
	return count > 0
}

// MarkSeen records hardKey as seen for sourceID in the Valkey bitmap.
// If Valkey is unavailable this is a no-op — the DB insert is the source of truth.
func MarkSeen(ctx context.Context, f *Filter, sourceID int64, hardKey string) {
	if f.valkey == nil {
		return
	}
	key := fmt.Sprintf("bloom:seen:%d", sourceID)
	offset := bloomOffset(hardKey)
	f.valkey.SetBit(ctx, key, offset, 1)
}

// Close releases the Valkey connection, if any.
func (f *Filter) Close() error {
	if f.valkey != nil {
		return f.valkey.Close()
	}
	return nil
}
