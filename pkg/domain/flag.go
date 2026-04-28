// Package domain — opportunity flagging.
//
// Logged-in users can flag a canonical opportunity as suspicious. The
// flagging surface is intentionally tiny (one row per user × slug);
// operators review the queue via /admin/flags and either ignore, hide,
// or trace the bad listing back to its source and ban that source.
//
// Three or more distinct unresolved scam flags on the same slug trigger
// auto-action: an OpportunityAutoFlaggedV1 event is emitted and the
// materializer drops the row from search by setting quality_score=0
// and pushing deadline to now(). Operator review still happens — the
// auto-action is a containment measure, not a final verdict.
package domain

import "time"

// FlagReason is the controlled vocabulary a user picks from when
// flagging an opportunity. Free-text goes in Description; this column
// is what the threshold check, top-flagged dashboards, and operator
// filters key off.
type FlagReason string

const (
	FlagScam      FlagReason = "scam"
	FlagExpired   FlagReason = "expired"
	FlagDuplicate FlagReason = "duplicate"
	FlagSpam      FlagReason = "spam"
	FlagOther     FlagReason = "other"
)

// IsKnownFlagReason reports whether r is one of the documented reasons.
// Used by the user POST /opportunities/{slug}/flag handler to validate
// the request body before persisting.
func IsKnownFlagReason(r FlagReason) bool {
	switch r {
	case FlagScam, FlagExpired, FlagDuplicate, FlagSpam, FlagOther:
		return true
	}
	return false
}

// OpportunityFlag is a user-submitted report on a single canonical
// opportunity. Multiple flags on the same opportunity from distinct
// users trigger auto-action via the threshold rule in the api's
// flag handler (≥3 distinct unresolved scam flags → emit
// OpportunityAutoFlaggedV1 → materializer drops the row from search).
//
// Idempotency: a unique index over (opportunity_slug, submitted_by)
// makes a duplicate flag from the same user a 409 instead of double-
// counting toward the threshold.
type OpportunityFlag struct {
	BaseModel
	OpportunitySlug  string     `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_flag_slug_user" json:"opportunity_slug"`
	OpportunityKind  string     `gorm:"type:varchar(40);not null" json:"opportunity_kind"`
	SubmittedBy      string     `gorm:"type:varchar(64);not null;index;uniqueIndex:idx_flag_slug_user" json:"submitted_by"` // candidate profile_id from JWT
	Reason           FlagReason `gorm:"type:varchar(20);not null;index" json:"reason"`
	Description      string     `gorm:"type:text" json:"description"` // free-text, max 1000 chars
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy       string     `gorm:"type:varchar(64)" json:"resolved_by,omitempty"`
	ResolutionAction string     `gorm:"type:varchar(20)" json:"resolution_action,omitempty"` // "ignore" | "hide" | "ban_source"
}

// TableName overrides the default GORM pluralisation. Keeps the table
// name aligned with the rest of the schema (sources, crawl_jobs, etc.).
func (OpportunityFlag) TableName() string { return "opportunity_flags" }

// FlagDescriptionMaxLen caps the free-text description length. Enforced
// in the user-facing POST handler before the row hits the database.
const FlagDescriptionMaxLen = 1000

// FlagAutoActionThreshold is the number of distinct unresolved scam
// flags from distinct users that triggers automatic containment on a
// slug (emit OpportunityAutoFlaggedV1; materializer hides the row).
const FlagAutoActionThreshold = 3

// FlagResolutionAction is the controlled vocabulary an operator picks
// when resolving a flag.
type FlagResolutionAction string

const (
	FlagActionIgnore    FlagResolutionAction = "ignore"
	FlagActionHide      FlagResolutionAction = "hide"
	FlagActionBanSource FlagResolutionAction = "ban_source"
)

// IsKnownFlagAction reports whether a is one of the documented values.
func IsKnownFlagAction(a FlagResolutionAction) bool {
	switch a {
	case FlagActionIgnore, FlagActionHide, FlagActionBanSource:
		return true
	}
	return false
}
