// Package autoapply provides a tiered application-submission strategy.
// Tier order: ATS-specific UI handlers (Greenhouse, Lever, Workday,
// SmartRecruiters) → LLM-guided generic form fill → email fallback →
// skip with nudge. The Registry tries submitters in registration order
// and uses the first whose CanHandle returns true.
package autoapply

import (
	"context"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Submitter handles application submission for one class of apply URL.
type Submitter interface {
	// Name returns a short, low-cardinality label used in logs and metrics
	// ("greenhouse_ui", "lever_ui", "llm_form", "email", etc.).
	Name() string
	// CanHandle returns true when this submitter can process the given
	// source type / URL combination.
	CanHandle(sourceType domain.SourceType, applyURL string) bool
	// Submit attempts submission. A non-nil error signals a transient
	// failure that the queue layer should redeliver. Terminal failures
	// (CAPTCHA wall, unsupported page, missing fields) are returned as
	// Method="skipped" in SubmitResult with a nil error.
	Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error)
}

// SubmitRequest carries everything a submitter needs to fill and submit
// a job application. All candidate data is packed by the matching service
// at intent-creation time so the autoapply service is read-only with
// respect to Postgres and Iceberg.
type SubmitRequest struct {
	SourceType   domain.SourceType
	ApplyURL     string
	CandidateID  string
	FullName     string
	Email        string
	Phone        string
	Location     string
	CurrentTitle string
	Skills       string
	CoverLetter  string
	CVBytes      []byte // nil when not available; submitters handle gracefully
	CVFilename   string // "resume.pdf" etc.
}

// SubmitResult describes the outcome of a submission attempt.
type SubmitResult struct {
	// Method is one of: "ats_ui", "llm_form", "email", "skipped".
	Method string
	// ExternalRef is the ATS application reference ID when the ATS
	// returns one (e.g. Greenhouse application_id). Empty otherwise.
	ExternalRef string
	// SkipReason is a short, low-cardinality string populated when
	// Method=="skipped" (e.g. "captcha", "unsupported", "no_submitter",
	// "no_smtp", "empty_url").
	SkipReason string
}
