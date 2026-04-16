package bloom

import (
	"context"
	"fmt"
	"hash/crc32"
	"hash/fnv"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"stawi.jobs/pkg/domain"
)

const bitmapSize = 1 << 20 // 1M bits per source (~128KB, <1% FPR at 50K items)
const numHashes = 3

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

// bloomOffsets returns k independent hash offsets for a key.
func bloomOffsets(key string) []int64 {
	data := []byte(key)
	h1 := crc32.ChecksumIEEE(data)
	h2 := fnv.New32a()
	h2.Write(data)
	base1 := int64(h1 % uint32(bitmapSize))
	base2 := int64(h2.Sum32() % uint32(bitmapSize))
	offsets := make([]int64, numHashes)
	for i := 0; i < numHashes; i++ {
		offsets[i] = (base1 + int64(i)*base2) % int64(bitmapSize)
		if offsets[i] < 0 {
			offsets[i] = -offsets[i]
		}
	}
	return offsets
}

// IsSeen reports whether hardKey has been seen for the given sourceID.
//  1. If Valkey is available: check the bit; a set bit means seen.
//  2. Otherwise (or when the bit is clear): query job_variants for a matching hard_key.
func IsSeen(ctx context.Context, f *Filter, sourceID int64, hardKey string) bool {
	if f.valkey != nil {
		key := fmt.Sprintf("bloom:seen:%d", sourceID)
		offsets := bloomOffsets(hardKey)
		allSet := true
		for _, offset := range offsets {
			bit, err := f.valkey.GetBit(ctx, key, offset).Result()
			if err != nil || bit != 1 {
				allSet = false
				break
			}
		}
		if allSet {
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
	offsets := bloomOffsets(hardKey)
	for _, offset := range offsets {
		f.valkey.SetBit(ctx, key, offset, 1)
	}
}

// Close releases the Valkey connection, if any.
func (f *Filter) Close() error {
	if f.valkey != nil {
		return f.valkey.Close()
	}
	return nil
}
