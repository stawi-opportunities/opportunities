package ats

import "fmt"

// Stage keys used by default templates.
const (
	StageApplied   = "applied"
	StageScreen    = "screen"
	StageInterview = "interview"
	StageOffer     = "offer"
	StageHired     = "hired"
	StageRejected  = "rejected"
	StageWithdrawn = "withdrawn"
)

// DefaultStages is the ordered happy-path pipeline.
func DefaultStages() []string {
	return []string{StageApplied, StageScreen, StageInterview, StageOffer, StageHired}
}

var transitions = map[string][]string{
	StageApplied: {
		StageScreen, StageRejected, StageWithdrawn,
	},
	StageScreen: {
		StageInterview, StageRejected, StageWithdrawn,
	},
	StageInterview: {
		StageOffer, StageRejected, StageWithdrawn,
	},
	StageOffer: {
		StageHired, StageRejected, StageWithdrawn,
	},
	StageHired:     {},
	StageRejected:  {},
	StageWithdrawn: {},
}

// IsTerminal reports whether no further advance is allowed.
func IsTerminal(stage string) bool {
	next, ok := transitions[stage]
	return ok && len(next) == 0
}

// AllowedNext returns copy of allowed next stages.
func AllowedNext(from string) []string {
	out := transitions[from]
	cp := make([]string, len(out))
	copy(cp, out)
	return cp
}

// ValidateAdvance returns nil if from→to is allowed.
func ValidateAdvance(from, to string) error {
	if from == to {
		return fmt.Errorf("ats: advance: already at %q", from)
	}
	for _, n := range transitions[from] {
		if n == to {
			return nil
		}
	}
	if _, ok := transitions[from]; !ok {
		return fmt.Errorf("ats: advance: unknown stage %q", from)
	}
	return fmt.Errorf("ats: advance: %q → %q not allowed", from, to)
}
