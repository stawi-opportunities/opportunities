package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Crawl-dispatch observability. Both the 2026-06-03 and 2026-06-11 crawl
// outages were silent because nothing counted dispatches: the pipeline
// can stop crawling entirely with every component "healthy". These
// counters make "zero crawls dispatched" and "gate closed" alertable.
var (
	CrawlDispatches    metric.Int64Counter
	CrawlCompletions   metric.Int64Counter
	CrawlSilentLoss    metric.Int64Counter
	SourceHealthEvents metric.Int64Counter
	RetentionExpired   metric.Int64Counter
)

// InitCrawl registers the crawl-pipeline metrics. Called from Init().
func InitCrawl() error {
	var err error

	CrawlDispatches, err = meter.Int64Counter("crawl.dispatch.total",
		metric.WithDescription("Crawl dispatch attempts by outcome (emitted/denied/skipped) and driver (schedule/overdue)"),
	)
	if err != nil {
		return err
	}

	CrawlCompletions, err = meter.Int64Counter("crawl.completed.total",
		metric.WithDescription("Completed crawls by outcome (success/failure)"),
	)
	if err != nil {
		return err
	}

	CrawlSilentLoss, err = meter.Int64Counter("crawl.silent_loss.total",
		metric.WithDescription("Best-effort crawl paths that dropped data"),
	)
	if err != nil {
		return err
	}

	SourceHealthEvents, err = meter.Int64Counter("crawl.source.health.total",
		metric.WithDescription("Source health transitions (failure/recovered/degraded/needs_tuning)"),
	)
	if err != nil {
		return err
	}

	RetentionExpired, err = meter.Int64Counter("retention.jobs.expired",
		metric.WithDescription("Opportunities hidden by the stale-job retention sweep"),
	)
	return err
}

// RecordCrawlDispatch counts one dispatch attempt. outcome is
// "emitted", "denied" (backpressure) or "skipped" (inactive source);
// driver is "schedule" (per-source Trustage cron) or "overdue" (catch-up
// sweep).
func RecordCrawlDispatch(outcome, driver string) {
	if CrawlDispatches == nil {
		return
	}
	CrawlDispatches.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("outcome", outcome),
			attribute.String("driver", driver),
		))
}

// RecordCrawlCompletion counts one finished crawl. outcome is "success"
// or "failure".
func RecordCrawlCompletion(outcome string) {
	if CrawlCompletions == nil {
		return
	}
	CrawlCompletions.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordCrawlSilentLoss counts a best-effort drop that previously left no
// signal. kind is a short, low-cardinality tag: "detail_fetch_skip",
// Values are bounded failure-reason labels defined by callers.
func RecordCrawlSilentLoss(kind string) {
	if CrawlSilentLoss == nil {
		return
	}
	CrawlSilentLoss.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("kind", kind)))
}

// RecordSourceHealthEvent counts a source health transition. event is
// "failure", "recovered", "degraded" or "needs_tuning".
func RecordSourceHealthEvent(event string) {
	if SourceHealthEvents == nil {
		return
	}
	SourceHealthEvents.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("event", event)))
}

// RecordRetentionExpired counts opportunities hidden by the stale-job
// sweep.
func RecordRetentionExpired(n int64) {
	if RetentionExpired == nil || n <= 0 {
		return
	}
	RetentionExpired.Add(context.Background(), n)
}
