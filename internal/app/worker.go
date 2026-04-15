package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"stawi.jobs/internal/config"
	"stawi.jobs/internal/connectors"
	"stawi.jobs/internal/dedupe"
	"stawi.jobs/internal/domain"
	"stawi.jobs/internal/indexer"
	"stawi.jobs/internal/normalize"
	"stawi.jobs/internal/queue"
)

type Worker struct {
	cfg      config.Config
	store    domain.UnitOfWork
	registry *connectors.Registry
	queue    queue.Queue
	indexer  indexer.Indexer
	log      *slog.Logger
}

func NewWorker(cfg config.Config, store domain.UnitOfWork, registry *connectors.Registry, q queue.Queue, idx indexer.Indexer, log *slog.Logger) *Worker {
	return &Worker{cfg: cfg, store: store, registry: registry, queue: q, indexer: idx, log: log}
}

func (w *Worker) Run(ctx context.Context) error {
	stream, err := w.queue.Consume(ctx)
	if err != nil {
		return fmt.Errorf("consume queue: %w", err)
	}
	engine := dedupe.New(w.store)
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.cfg.WorkerConcurrency)
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case req, ok := <-stream:
			if !ok {
				wg.Wait()
				return nil
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(r domain.CrawlRequest) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := w.handleRequest(ctx, engine, r); err != nil {
					w.log.Error("crawl request failed", "source_id", r.SourceID, "source_type", r.SourceType, "error", err.Error())
				}
			}(req)
		}
	}
}

func (w *Worker) handleRequest(ctx context.Context, engine *dedupe.Engine, req domain.CrawlRequest) error {
	src, err := w.store.GetSource(ctx, req.SourceID)
	if err != nil {
		return fmt.Errorf("get source: %w", err)
	}
	connector, ok := w.registry.Get(src.Type)
	if !ok {
		return fmt.Errorf("connector not registered for source type %s", src.Type)
	}
	jobs, rawBody, status, err := connector.Crawl(ctx, src)
	if err != nil {
		return fmt.Errorf("connector crawl: %w", err)
	}
	if _, err := w.store.StoreRawPayload(ctx, domain.RawPayload{
		CrawlJobID:  0,
		StorageURI:  "db://raw_payloads",
		ContentHash: domain.BuildHardKey(src.BaseURL, src.Country, fmt.Sprintf("%d", status), time.Now().UTC().Format(time.RFC3339Nano)),
		FetchedAt:   time.Now().UTC(),
		HTTPStatus:  status,
		Body:        rawBody,
	}); err != nil {
		w.log.Warn("store raw payload failed", "source_id", src.ID, "error", err.Error())
	}
	for _, ext := range jobs {
		variant := normalize.ExternalToVariant(ext, src.ID, src.Country, time.Now().UTC())
		canonical, err := engine.UpsertAndCluster(ctx, variant)
		if err != nil {
			w.log.Error("dedupe upsert failed", "source_id", src.ID, "external_id", ext.ExternalID, "error", err.Error())
			continue
		}
		if err := w.indexer.IndexCanonicalJob(ctx, canonical); err != nil {
			w.log.Warn("index canonical failed", "cluster_id", canonical.ClusterID, "error", err.Error())
		}
	}
	next := time.Now().UTC().Add(time.Duration(src.CrawlIntervalSec) * time.Second)
	if src.CrawlIntervalSec <= 0 {
		next = time.Now().UTC().Add(12 * time.Hour)
	}
	_ = w.store.TouchSource(ctx, src.ID, next, 1.0)
	w.log.Info("source processed", "source_id", src.ID, "source_type", src.Type, "jobs", len(jobs))
	return nil
}
