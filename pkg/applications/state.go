// Package applications encapsulates the rules of the applications
// domain: the state machine (this file) and the autonomy-rules
// document schema (rules.go). Pure code only — no DB, no HTTP.
package applications

import "fmt"

// Status is the lifecycle state of an application.
type Status string

const (
	StatusNew       Status = "new"
	StatusDismissed Status = "dismissed"
	StatusApplying  Status = "applying"
	StatusSubmitted Status = "submitted"
	StatusScreening Status = "screening"
	StatusInterview Status = "interview"
	StatusOffer     Status = "offer"
	StatusAccepted  Status = "accepted"
	StatusRejected  Status = "rejected"
	StatusWithdrawn Status = "withdrawn"
)

// transitions encodes the allowed-next states for each status.
// Single source of truth — referenced by ValidateTransition and
// AllowedNext.
var transitions = map[Status][]Status{
	StatusNew: {
		StatusDismissed,
		StatusApplying,
	},
	StatusApplying: {
		StatusSubmitted,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusSubmitted: {
		StatusScreening,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusScreening: {
		StatusInterview,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusInterview: {
		StatusOffer,
		StatusRejected,
		StatusWithdrawn,
	},
	StatusOffer: {
		StatusAccepted,
		StatusRejected,
		StatusWithdrawn,
	},
	// Terminal states have no outgoing edges.
	StatusDismissed: {},
	StatusAccepted:  {},
	StatusRejected:  {},
	StatusWithdrawn: {},
}

// terminalSet for fast IsTerminal checks.
var terminalSet = map[Status]bool{
	StatusDismissed: true,
	StatusAccepted:  true,
	StatusRejected:  true,
	StatusWithdrawn: true,
}

// IsTerminal reports whether the status admits no further transitions.
func (s Status) IsTerminal() bool {
	return terminalSet[s]
}

// AllStatuses returns every defined status. Useful for enumeration in
// validators and tests.
func AllStatuses() []Status {
	return []Status{
		StatusNew,
		StatusDismissed,
		StatusApplying,
		StatusSubmitted,
		StatusScreening,
		StatusInterview,
		StatusOffer,
		StatusAccepted,
		StatusRejected,
		StatusWithdrawn,
	}
}

// AllowedNext returns the list of statuses a row currently in `from`
// is allowed to transition to. Empty slice for terminal or unknown.
func AllowedNext(from Status) []Status {
	out := transitions[from]
	cp := make([]Status, len(out))
	copy(cp, out)
	return cp
}

// InvalidTransitionError reports an attempt to move between states
// that the transition table forbids. Carries the allowed set so HTTP
// handlers can put it in the 409 response body.
type InvalidTransitionError struct {
	From    Status
	To      Status
	Allowed []Status
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf(
		"applications: invalid transition %s → %s (allowed: %v)",
		e.From, e.To, e.Allowed,
	)
}

// ValidateTransition returns nil if `from → to` is allowed by the
// transition table, otherwise an *InvalidTransitionError.
func ValidateTransition(from, to Status) error {
	allowed, known := transitions[from]
	if !known {
		return &InvalidTransitionError{From: from, To: to}
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return &InvalidTransitionError{From: from, To: to, Allowed: allowed}
}
