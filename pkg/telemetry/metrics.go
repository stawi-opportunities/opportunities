package telemetry

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("stawi.jobs.pipeline")

	StageTransitions metric.Int64Counter
	StageDuration    metric.Float64Histogram
	BloomHits        metric.Int64Counter
	BloomMisses      metric.Int64Counter
	JobsReady        metric.Int64Counter
	AIExtractions    metric.Int64Counter
	AIFailures       metric.Int64Counter
)

// Init registers all pipeline metrics with the global OTel meter provider.
// It should be called once early in main(), before any pipeline handlers run.
func Init() error {
	var err error

	StageTransitions, err = meter.Int64Counter("pipeline.stage.transitions",
		metric.WithDescription("Number of stage transitions"),
	)
	if err != nil {
		return err
	}

	StageDuration, err = meter.Float64Histogram("pipeline.stage.duration_seconds",
		metric.WithDescription("Duration of each pipeline stage"),
	)
	if err != nil {
		return err
	}

	BloomHits, err = meter.Int64Counter("pipeline.bloom.hits",
		metric.WithDescription("Bloom filter positive lookups (skipped)"),
	)
	if err != nil {
		return err
	}

	BloomMisses, err = meter.Int64Counter("pipeline.bloom.misses",
		metric.WithDescription("Bloom filter negative lookups (new jobs)"),
	)
	if err != nil {
		return err
	}

	JobsReady, err = meter.Int64Counter("pipeline.jobs.ready",
		metric.WithDescription("Jobs that reached ready stage"),
	)
	if err != nil {
		return err
	}

	AIExtractions, err = meter.Int64Counter("pipeline.ai.extractions",
		metric.WithDescription("AI extraction attempts"),
	)
	if err != nil {
		return err
	}

	AIFailures, err = meter.Int64Counter("pipeline.ai.failures",
		metric.WithDescription("AI extraction failures"),
	)
	if err != nil {
		return err
	}

	return InitIceberg()
}
