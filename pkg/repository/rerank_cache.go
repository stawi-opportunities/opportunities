package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stawi.jobs/pkg/domain"
)

// RerankCacheRepository stores reranker scores keyed by a content hash so we
// don't pay to re-score the same (CV, job) pair across weekly cron sweeps.
type RerankCacheRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func NewRerankCacheRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *RerankCacheRepository {
	return &RerankCacheRepository{db: db}
}

// RerankCacheKey builds a stable content-hash cache key. Including the
// reranker version means model swaps invalidate the cache automatically
// instead of returning stale scores from an old model.
func RerankCacheKey(cvText string, canonicalJobID int64, rerankerVersion string) string {
	h := sha256.New()
	h.Write([]byte(cvText))
	h.Write([]byte{0x1f}) // unit-separator so concatenation is unambiguous
	h.Write([]byte(itoa(canonicalJobID)))
	h.Write([]byte{0x1f})
	h.Write([]byte(rerankerVersion))
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns (score, true, nil) on hit, (0, false, nil) on miss, error
// only for real database failures. A hit increments the Hits counter in a
// fire-and-forget goroutine so the read path stays single-round-trip.
func (r *RerankCacheRepository) Get(ctx context.Context, key string) (float32, bool, error) {
	var row domain.RerankCache
	err := r.db(ctx, true).
		Where("cache_key = ?", key).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	go func() {
		// Best-effort: a stats miss doesn't invalidate the hit itself.
		_ = r.db(context.Background(), false).
			Model(&domain.RerankCache{}).
			Where("cache_key = ?", key).
			UpdateColumn("hits", gorm.Expr("hits + 1")).Error
	}()
	return row.Score, true, nil
}

// Put upserts a score. Idempotent on the same (key, version).
func (r *RerankCacheRepository) Put(ctx context.Context, key string, score float32, version string) error {
	entry := domain.RerankCache{
		CacheKey:        key,
		Score:           score,
		RerankerVersion: version,
		CreatedAt:       time.Now(),
	}
	return r.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "cache_key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"score", "reranker_version", "created_at",
			}),
		}).
		Create(&entry).Error
}

// PurgeOlderThan drops cache rows older than age. Safe to run from the
// retention cron. Returns rows removed.
func (r *RerankCacheRepository) PurgeOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	cutoff := time.Now().Add(-age)
	res := r.db(ctx, false).
		Where("created_at < ?", cutoff).
		Delete(&domain.RerankCache{})
	return res.RowsAffected, res.Error
}

// itoa is a local base-10 formatter to avoid pulling strconv for a single
// call in the hot cache-key path.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
