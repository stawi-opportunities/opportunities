package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("stawi.opportunities.pipeline")

	StageTransitions   metric.Int64Counter
	StageDuration      metric.Float64Histogram
	BloomHits          metric.Int64Counter
	BloomMisses        metric.Int64Counter
	OpportunitiesReady metric.Int64Counter
	VerifyRejections   metric.Int64Counter
	ExtractionLatency  metric.Float64Histogram
	AIExtractions      metric.Int64Counter
	AIFailures         metric.Int64Counter
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

	OpportunitiesReady, err = meter.Int64Counter("pipeline.opportunities.ready",
		metric.WithDescription("Opportunities that reached ready stage (per kind)"),
	)
	if err != nil {
		return err
	}

	VerifyRejections, err = meter.Int64Counter("pipeline.verify.rejections",
		metric.WithDescription("Variants rejected by opportunity.Verify (per kind/reason)"),
	)
	if err != nil {
		return err
	}

	ExtractionLatency, err = meter.Float64Histogram("pipeline.extraction.latency_seconds",
		metric.WithDescription("Extraction stage latency in seconds (per kind)"),
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

// RecordOpportunityReady increments the per-kind ready counter. Safe to
// call before Init (e.g. from tests); the no-op branch keeps unwired
// callsites from panicking.
func RecordOpportunityReady(kind string) {
	if OpportunitiesReady == nil {
		return
	}
	OpportunitiesReady.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("kind", kind)))
}

// RecordVerifyRejection increments the verify-rejection counter with
// the kind and the categorised reason ("mismatch", "missing_<field>",
// "unknown").
func RecordVerifyRejection(kind, reason string) {
	if VerifyRejections == nil {
		return
	}
	VerifyRejections.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("kind", kind),
			attribute.String("reason", reason),
		))
}

// RecordExtractionLatency observes the extraction-stage duration for a
// given kind. seconds is the elapsed time in seconds (use
// time.Since(t).Seconds()).
func RecordExtractionLatency(kind string, seconds float64) {
	if ExtractionLatency == nil {
		return
	}
	ExtractionLatency.Record(context.Background(), seconds,
		metric.WithAttributes(attribute.String("kind", kind)))
}
