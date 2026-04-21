package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/eventlog"
	"stawi.jobs/pkg/repository"
)

// Service drives the materializer's main poll loop. Each tick it
// asks R2 for new objects under each tracked prefix (since last
// watermark), downloads + applies them to Manticore, and advances
// the watermark on success.
type Service struct {
	reader       *eventlog.Reader
	indexer      *Indexer
	watermarks   *repository.WatermarkRepository
	prefixes     []string
	pollInterval time.Duration
	listBatch    int32
}

// NewService assembles the materializer.
func NewService(
	reader *eventlog.Reader,
	indexer *Indexer,
	watermarks *repository.WatermarkRepository,
	prefixes []string,
	pollInterval time.Duration,
	listBatch int32,
) *Service {
	return &Service{
		reader:       reader,
		indexer:      indexer,
		watermarks:   watermarks,
		prefixes:     prefixes,
		pollInterval: pollInterval,
		listBatch:    listBatch,
	}
}

// Run polls until ctx is cancelled. Errors are logged + swallowed so
// one broken prefix doesn't stall the others.
func (s *Service) Run(ctx context.Context) error {
	t := time.NewTicker(s.pollInterval)
	defer t.Stop()

	// First tick fires immediately — skips one idle interval on boot.
	if err := s.pollOnce(ctx); err != nil {
		util.Log(ctx).WithError(err).Warn("materializer: initial poll failed")
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.pollOnce(ctx); err != nil {
				util.Log(ctx).WithError(err).Warn("materializer: poll failed")
			}
		}
	}
}

func (s *Service) pollOnce(ctx context.Context) error {
	for _, p := range s.prefixes {
		if err := s.pollPrefix(ctx, p); err != nil {
			util.Log(ctx).WithError(err).WithField("prefix", p).Error("materializer: prefix poll failed")
		}
	}
	return nil
}

func (s *Service) pollPrefix(ctx context.Context, prefix string) error {
	lastKey, err := s.watermarks.Get(ctx, prefix)
	if err != nil {
		return fmt.Errorf("get watermark: %w", err)
	}
	objs, err := s.reader.ListNewObjects(ctx, prefix, lastKey, s.listBatch)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(objs) == 0 {
		return nil
	}

	for _, o := range objs {
		key := ""
		if o.Key != nil {
			key = *o.Key
		}
		body, err := s.reader.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("get %s: %w", key, err)
		}

		switch {
		case strings.HasPrefix(prefix, "canonicals"):
			if _, err := s.indexer.ApplyCanonicalsParquet(ctx, body); err != nil {
				return fmt.Errorf("apply canonicals %s: %w", key, err)
			}
		case strings.HasPrefix(prefix, "embeddings"):
			if _, err := s.indexer.ApplyEmbeddingsParquet(ctx, body); err != nil {
				return fmt.Errorf("apply embeddings %s: %w", key, err)
			}
		default:
			util.Log(ctx).WithField("prefix", prefix).Warn("materializer: unknown prefix, skipping")
		}

		if err := s.watermarks.Set(ctx, prefix, key); err != nil {
			return fmt.Errorf("advance watermark to %s: %w", key, err)
		}
	}
	return nil
}
