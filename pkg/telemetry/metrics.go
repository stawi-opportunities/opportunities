package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("stawi.opportunities.pipeline")

	OpportunitiesReady      metric.Int64Counter
	VerifyRejections        metric.Int64Counter
	ExtractionLatency       metric.Float64Histogram
	AIExtractions           metric.Int64Counter
	AIFailures              metric.Int64Counter
	PreferenceTriggeredRuns metric.Int64Counter
)

// Init registers all pipeline metrics with the global OTel meter provider.
// It should be called once early in main(), before any pipeline handlers run.
func Init() error {
	var err error

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

	PreferenceTriggeredRuns, err = meter.Int64Counter("pipeline.matching.preference_triggered_runs",
		metric.WithDescription("Match runs triggered by a preferences-updated event, labelled by kind"),
	)
	if err != nil {
		return err
	}

	if err = InitCrawl(); err != nil {
		return err
	}

	return nil
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

// RecordPreferenceTriggeredRun increments the preference-triggered-runs
// counter. kind is the matcher kind whose preferences fired the rerun.
func RecordPreferenceTriggeredRun(kind string) {
	if PreferenceTriggeredRuns == nil {
		return
	}
	PreferenceTriggeredRuns.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("kind", kind)))
}
