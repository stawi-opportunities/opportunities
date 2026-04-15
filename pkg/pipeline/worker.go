package pipeline

import (
	"context"
	"time"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/dedupe"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/normalize"
	"stawi.jobs/pkg/quality"
	"stawi.jobs/pkg/repository"
)

// CrawlResult summarises the outcome of processing a single CrawlRequest.
type CrawlResult struct {
	SourceID     int64
	SourceType   domain.SourceType
	JobsFetched  int
	JobsAccepted int
	JobsRejected int
	PagesCrawled int
	Duration     time.Duration
	Error        error
}

// Worker processes a single CrawlRequest through the full pipeline:
// fetch → quality gate → normalize → batch write.
type Worker struct {
	registry   *connectors.Registry
	sourceRepo *repository.SourceRepository
	crawlRepo  *repository.CrawlRepository
	jobRepo    *repository.JobRepository
	rejectRepo *repository.RejectedJobRepository
	dedupeEng  *dedupe.Engine
	batch      *BatchBuffer
}

// NewWorker creates a Worker wired to the given repositories, registry, dedupe
// engine, and batch buffer.
func NewWorker(
	registry *connectors.Registry,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	jobRepo *repository.JobRepository,
	rejectRepo *repository.RejectedJobRepository,
	dedupeEng *dedupe.Engine,
	batch *BatchBuffer,
) *Worker {
	return &Worker{
		registry:   registry,
		sourceRepo: sourceRepo,
		crawlRepo:  crawlRepo,
		jobRepo:    jobRepo,
		rejectRepo: rejectRepo,
		dedupeEng:  dedupeEng,
		batch:      batch,
	}
}

// ProcessRequest executes the crawl pipeline for the given CrawlRequest and
// returns a CrawlResult describing the outcome.
func (w *Worker) ProcessRequest(ctx context.Context, req domain.CrawlRequest) CrawlResult {
	start := time.Now()
	result := CrawlResult{
		SourceID:   req.SourceID,
		SourceType: req.SourceType,
	}

	// 1. Load source record.
	source, err := w.sourceRepo.GetByID(ctx, req.SourceID)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}

	// 2. Resolve the connector for this source type.
	connector, ok := w.registry.Get(source.Type)
	if !ok {
		result.Error = &errNoConnector{sourceType: source.Type}
		result.Duration = time.Since(start)
		return result
	}

	// 3. Start crawl iterator.
	iter := connector.Crawl(ctx, *source)

	scrapedAt := time.Now().UTC()

	// 4. Iterate over pages.
	for iter.Next(ctx) {
		result.PagesCrawled++

		// Save raw payload for this page.
		raw := iter.RawPayload()
		if len(raw) > 0 {
			payload := &domain.RawPayload{
				HTTPStatus: iter.HTTPStatus(),
				Body:       raw,
				FetchedAt:  scrapedAt,
			}
			// Best-effort — ignore save errors to keep the pipeline moving.
			_ = w.crawlRepo.SaveRawPayload(ctx, payload)
		}

		// Process each job on this page.
		for _, extJob := range iter.Jobs() {
			result.JobsFetched++

			// Quality gate.
			if qErr := quality.Check(extJob); qErr != nil {
				rejected := &domain.RejectedJob{
					SourceID:   req.SourceID,
					ExternalID: extJob.ExternalID,
					Reason:     qErr.Error(),
					RejectedAt: scrapedAt,
				}
				_ = w.rejectRepo.Create(ctx, rejected)
				result.JobsRejected++
				continue
			}

			// Normalize.
			variant := normalize.ExternalToVariant(extJob, req.SourceID, source.Country, string(source.Type), scrapedAt)

			// Buffer for batch write.
			if bErr := w.batch.Add(ctx, variant); bErr != nil {
				// Non-fatal: record the error but continue processing.
				result.Error = bErr
			}
			result.JobsAccepted++
		}
	}

	// 5. Post-crawl health score and scheduling updates.
	now := time.Now().UTC()
	healthScore := source.HealthScore
	if iter.Err() != nil {
		result.Error = iter.Err()
		healthScore -= 0.2
		if healthScore < 0 {
			healthScore = 0
		}
	} else {
		healthScore += 0.1
		if healthScore > 1.0 {
			healthScore = 1.0
		}
	}

	nextCrawlAt := now.Add(time.Duration(source.CrawlIntervalSec) * time.Second)
	_ = w.sourceRepo.UpdateNextCrawl(ctx, source.ID, nextCrawlAt, now, healthScore)

	// Save cursor if the iterator produced one.
	if cursor := iter.Cursor(); cursor != nil {
		_ = w.sourceRepo.UpdateCrawlCursor(ctx, source.ID, string(cursor))
	}

	result.Duration = time.Since(start)
	return result
}

// errNoConnector is returned when the registry has no connector for a source type.
type errNoConnector struct {
	sourceType domain.SourceType
}

func (e *errNoConnector) Error() string {
	return "no connector registered for source type: " + string(e.sourceType)
}
