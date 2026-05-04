package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("stawi.opportunities.pipeline")

	StageTransitions          metric.Int64Counter
	StageDuration             metric.Float64Histogram
	BloomHits                 metric.Int64Counter
	BloomMisses               metric.Int64Counter
	OpportunitiesReady        metric.Int64Counter
	VerifyRejections          metric.Int64Counter
	ExtractionLatency         metric.Float64Histogram
	AIExtractions             metric.Int64Counter
	AIFailures                metric.Int64Counter
	EmbedFailures             metric.Int64Counter
	TranslateFailures         metric.Int64Counter
	PreferenceTriggeredRuns   metric.Int64Counter

	AutoApplyAttempts        metric.Int64Counter
	AutoApplyLimitReached    metric.Int64Counter
	AutoApplySubmitDuration  metric.Float64Histogram
	AutoApplyCVDownloadFails metric.Int64Counter
	AutoApplyBrowserRestarts metric.Int64Counter
	AutoApplyCaptcha         metric.Int64Counter
	AutoApplyTransientErrors metric.Int64Counter
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

	EmbedFailures, err = meter.Int64Counter("pipeline.embed.failures",
		metric.WithDescription("Permanent embedding failures (LLM rate-limit, parse, etc.)"),
	)
	if err != nil {
		return err
	}

	TranslateFailures, err = meter.Int64Counter("pipeline.translate.failures",
		metric.WithDescription("Permanent translation failures, labelled by target language"),
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

	AutoApplyAttempts, err = meter.Int64Counter("autoapply.attempts",
		metric.WithDescription("Auto-apply submission attempts, labelled by status and method"),
	)
	if err != nil {
		return err
	}

	AutoApplyLimitReached, err = meter.Int64Counter("autoapply.daily_limit_reached",
		metric.WithDescription("Times the per-candidate daily auto-apply limit was reached"),
	)
	if err != nil {
		return err
	}

	AutoApplySubmitDuration, err = meter.Float64Histogram("autoapply.submit.duration_seconds",
		metric.WithDescription("End-to-end submission latency, labelled by method and status"),
	)
	if err != nil {
		return err
	}

	AutoApplyCVDownloadFails, err = meter.Int64Counter("autoapply.cv_download.failures",
		metric.WithDescription("CV download failures, labelled by reason (network, http_status, too_large, blocked_url)"),
	)
	if err != nil {
		return err
	}

	AutoApplyBrowserRestarts, err = meter.Int64Counter("autoapply.browser.restarts",
		metric.WithDescription("Headless browser process restarts, labelled by reason (launch_failed, health_check, crashed)"),
	)
	if err != nil {
		return err
	}

	AutoApplyCaptcha, err = meter.Int64Counter("autoapply.captcha_detected",
		metric.WithDescription("CAPTCHA walls detected, labelled by host bucket"),
	)
	if err != nil {
		return err
	}

	AutoApplyTransientErrors, err = meter.Int64Counter("autoapply.transient_errors",
		metric.WithDescription("Transient infra errors that triggered a redelivery, labelled by stage"),
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

// RecordEmbedFailure increments the embed-failures counter. reason is a
// short, low-cardinality tag like "rate_limit", "parse", "network".
func RecordEmbedFailure(reason string) {
	if EmbedFailures == nil {
		return
	}
	EmbedFailures.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("reason", reason)))
}

// RecordTranslateFailure increments the translate-failures counter.
// lang is the target language code; reason is a short, low-cardinality
// tag like "rate_limit", "parse", "network".
func RecordTranslateFailure(lang, reason string) {
	if TranslateFailures == nil {
		return
	}
	TranslateFailures.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("lang", lang),
			attribute.String("reason", reason),
		))
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

// RecordAutoApplyAttempt increments the auto-apply attempt counter.
// status is one of "submitted", "skipped", "failed"; method is the
// submitter that handled it (e.g. "greenhouse_ui", "email_fallback").
func RecordAutoApplyAttempt(status, method string) {
	if AutoApplyAttempts == nil {
		return
	}
	AutoApplyAttempts.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("status", status),
			attribute.String("method", method),
		))
}

// RecordAutoApplyLimitReached increments the daily-limit counter.
func RecordAutoApplyLimitReached() {
	if AutoApplyLimitReached == nil {
		return
	}
	AutoApplyLimitReached.Add(context.Background(), 1)
}

// RecordAutoApplySubmitDuration records end-to-end submission latency.
// status is "submitted", "skipped", or "failed"; method is the submitter
// that handled it (e.g. "greenhouse_ui").
func RecordAutoApplySubmitDuration(seconds float64, status, method string) {
	if AutoApplySubmitDuration == nil {
		return
	}
	AutoApplySubmitDuration.Record(context.Background(), seconds,
		metric.WithAttributes(
			attribute.String("status", status),
			attribute.String("method", method),
		))
}

// RecordAutoApplyCVDownloadFailure increments the CV-download failure
// counter. reason is a low-cardinality tag.
func RecordAutoApplyCVDownloadFailure(reason string) {
	if AutoApplyCVDownloadFails == nil {
		return
	}
	AutoApplyCVDownloadFails.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("reason", reason)))
}

// RecordAutoApplyBrowserRestart increments the browser-restart counter.
func RecordAutoApplyBrowserRestart(reason string) {
	if AutoApplyBrowserRestarts == nil {
		return
	}
	AutoApplyBrowserRestarts.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("reason", reason)))
}

// RecordAutoApplyCaptcha increments the captcha-detected counter.
func RecordAutoApplyCaptcha(host string) {
	if AutoApplyCaptcha == nil {
		return
	}
	AutoApplyCaptcha.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("host", host)))
}

// RecordAutoApplyTransientError increments the transient-error counter.
// stage is one of "exists_check", "persist", "publish", "browser_init".
func RecordAutoApplyTransientError(stage string) {
	if AutoApplyTransientErrors == nil {
		return
	}
	AutoApplyTransientErrors.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("stage", stage)))
}
