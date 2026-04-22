// Package service drives the materializer's Iceberg snapshot-diff poll
// loop. Each tick, one goroutine per table scans for data files added
// since the last watermark, reads them via R2, decodes the Parquet rows,
// and bulk-upserts them into Manticore. The Valkey watermark is advanced
// only after a successful Manticore flush (atomic-ish commit).
package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/apache/iceberg-go/catalog"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/searchindex"
)

// Service is the materializer composition root.
type Service struct {
	catalog   catalog.Catalog
	reader    *eventlog.Reader // reads Parquet bytes from R2
	r2Bucket  string           // e.g. "stawi-jobs-log"
	manticore *searchindex.Client
	wm        *Watermark
	pollEvery time.Duration
	tables    []tableSink
}

// tableSink pairs an Iceberg table identifier with the function that
// transforms a decoded row into a Manticore document.
type tableSink struct {
	// Ident is the two-element slice used with catalog.LoadTable,
	// e.g. []string{"jobs","canonicals"}.
	Ident []string
	// apply decodes a typed row from raw Parquet bytes and calls the
	// bulk upserter. body is the full Parquet file; upserter is the
	// batched writer for this tick.
	apply func(ctx context.Context, body []byte, up *BulkUpserter) error
}

// NewService wires the materializer. r2Bucket is used to strip the
// s3://<bucket>/ prefix from Iceberg file paths before fetching via
// eventlog.Reader.
func NewService(
	cat catalog.Catalog,
	reader *eventlog.Reader,
	r2Bucket string,
	mc *searchindex.Client,
	wm *Watermark,
	poll time.Duration,
) *Service {
	s := &Service{
		catalog:   cat,
		reader:    reader,
		r2Bucket:  r2Bucket,
		manticore: mc,
		wm:        wm,
		pollEvery: poll,
	}
	s.tables = []tableSink{
		{Ident: []string{"jobs", "canonicals"}, apply: s.applyCanonicals},
		{Ident: []string{"jobs", "embeddings"}, apply: s.applyEmbeddings},
		{Ident: []string{"jobs", "translations"}, apply: s.applyTranslations},
	}
	return s
}

// Run drives the poll loop until ctx is cancelled.
func (s *Service) Run(ctx context.Context) error {
	// Fire once immediately so the pod doesn't idle for a full interval
	// on boot.
	s.tick(ctx)

	t := time.NewTicker(s.pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// tick fans out one goroutine per table.
func (s *Service) tick(ctx context.Context) {
	var wg sync.WaitGroup
	for _, sink := range s.tables {
		wg.Add(1)
		go func(sink tableSink) {
			defer wg.Done()
			if err := s.processTable(ctx, sink); err != nil {
				util.Log(ctx).WithError(err).
					WithField("table", sink.Ident).
					Error("materializer: table tick failed")
			}
		}(sink)
	}
	wg.Wait()
}

// processTable is the per-table pipeline: read watermark → scan diff →
// decode + bulk-upsert → advance watermark.
func (s *Service) processTable(ctx context.Context, sink tableSink) error {
	identKey := sink.Ident[0] + "." + sink.Ident[1]

	prev, err := s.wm.Get(ctx, identKey)
	if err != nil {
		return fmt.Errorf("read watermark %s: %w", identKey, err)
	}

	diff, err := ScanNewData(ctx, s.catalog, sink.Ident, prev)
	if err != nil {
		return fmt.Errorf("scan %s: %w", identKey, err)
	}
	if len(diff.Files) == 0 {
		return nil
	}

	upserter := NewBulkUpserter(s.manticore, "idx_jobs_rt", 500)

	for _, task := range diff.Files {
		key := icebergFileKey(task.File.FilePath(), s.r2Bucket)
		body, err := s.reader.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("fetch file %s: %w", key, err)
		}
		if err := sink.apply(ctx, body, upserter); err != nil {
			return fmt.Errorf("apply %s [%s]: %w", identKey, key, err)
		}
	}

	if err := upserter.Flush(ctx); err != nil {
		return fmt.Errorf("manticore flush %s: %w", identKey, err)
	}

	// Advance watermark only after confirmed flush.
	return s.wm.Set(ctx, identKey, diff.ToSnapID)
}

// icebergFileKey strips the s3://<bucket>/ prefix from an Iceberg file
// path, returning the bare R2 object key. If the path doesn't start
// with the expected prefix it is returned as-is (handles relative
// paths used in local/MinIO tests).
func icebergFileKey(filePath, bucket string) string {
	prefix := "s3://" + bucket + "/"
	if strings.HasPrefix(filePath, prefix) {
		return strings.TrimPrefix(filePath, prefix)
	}
	return filePath
}

// ---------------------------------------------------------------------------
// Per-table row decoders
// ---------------------------------------------------------------------------

func (s *Service) applyCanonicals(ctx context.Context, body []byte, up *BulkUpserter) error {
	rows, err := eventlog.ReadParquet[eventsv1.CanonicalUpsertedV1](body)
	if err != nil {
		return fmt.Errorf("decode canonicals parquet: %w", err)
	}
	for _, r := range rows {
		doc := map[string]any{
			"canonical_id":    r.CanonicalID,
			"slug":            r.Slug,
			"title":           r.Title,
			"company":         r.Company,
			"description":     r.Description,
			"location_text":   r.LocationText,
			"category":        r.Category,
			"country":         r.Country,
			"language":        r.Language,
			"remote_type":     r.RemoteType,
			"employment_type": r.EmploymentType,
			"seniority":       r.Seniority,
			"salary_min":      uint64(r.SalaryMin),
			"salary_max":      uint64(r.SalaryMax),
			"currency":        r.Currency,
			"quality_score":   float32(r.QualityScore),
			"is_featured":     r.QualityScore >= 80,
			"posted_at":       r.PostedAt.Unix(),
			"last_seen_at":    r.LastSeenAt.Unix(),
			"expires_at":      r.ExpiresAt.Unix(),
			"status":          r.Status,
		}
		if err := up.Add(ctx, hashID(r.CanonicalID), doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyEmbeddings(ctx context.Context, body []byte, up *BulkUpserter) error {
	rows, err := eventlog.ReadParquet[eventsv1.EmbeddingV1](body)
	if err != nil {
		return fmt.Errorf("decode embeddings parquet: %w", err)
	}
	for _, r := range rows {
		doc := map[string]any{
			"embedding":       r.Vector,
			"embedding_model": r.ModelVersion,
		}
		if err := up.Add(ctx, hashID(r.CanonicalID), doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyTranslations(ctx context.Context, body []byte, up *BulkUpserter) error {
	rows, err := eventlog.ReadParquet[eventsv1.TranslationV1](body)
	if err != nil {
		return fmt.Errorf("decode translations parquet: %w", err)
	}
	for _, r := range rows {
		// Translations patch the title/description in the target language.
		// The document key includes the language suffix so per-lang rows
		// are independent. We stable-hash "canonical_id:lang" to get a
		// unique Manticore row id per (canonical, language).
		doc := map[string]any{
			"canonical_id":   r.CanonicalID,
			"lang":           r.Lang,
			"title":          r.TitleTr,
			"description":    r.DescriptionTr,
			"model_version":  r.ModelVersion,
		}
		if err := up.Add(ctx, hashID(r.CanonicalID+":"+r.Lang), doc); err != nil {
			return err
		}
	}
	return nil
}
