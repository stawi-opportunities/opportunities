package matching

import "time"

// MatchStatus is the lifecycle state of a row in candidate_matches.
//   new       — written by a matcher path, never read by the user
//   viewed    — extension reported the user saw it
//   dismissed — user (or rule) dismissed; terminal
//   applying  — extension started the apply flow
//   applied   — application submitted; terminal
//   overflow  — beyond the candidate's daily_cap; hidden from default polls
type MatchStatus string

const (
	StatusNew       MatchStatus = "new"
	StatusViewed    MatchStatus = "viewed"
	StatusDismissed MatchStatus = "dismissed"
	StatusApplying  MatchStatus = "applying"
	StatusApplied   MatchStatus = "applied"
	StatusOverflow  MatchStatus = "overflow"
)

// IsTerminal reports whether the state must never be downgraded.
// Idempotency invariant: write-paths use ON CONFLICT ... WHERE status='new'
// to refuse downgrades into terminals (dismissed/applied).
func (s MatchStatus) IsTerminal() bool {
	switch s {
	case StatusDismissed, StatusApplied:
		return true
	}
	return false
}

// AllStatuses returns the enumerated status set in a stable order.
func AllStatuses() []MatchStatus {
	return []MatchStatus{
		StatusNew, StatusViewed, StatusDismissed,
		StatusApplying, StatusApplied, StatusOverflow,
	}
}

// Match is one row of candidate_matches.
type Match struct {
	MatchID       string
	CandidateID   string
	OpportunityID string
	Status        MatchStatus
	Score         float64
	RerankScore   *float64
	RerankerUsed  bool
	ViewedAt      *time.Time
	AppliedAt     *time.Time
	DismissedAt   *time.Time
	LastEventID   string
	Metadata      map[string]any
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
