package applications_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

// TestState_AnyValidSequenceLandsInReachable asserts that any path of
// valid transitions starting at StatusNew lands in a state the
// transition graph declares reachable from StatusNew. This catches
// accidental graph holes (e.g. a state with no incoming edges).
func TestState_AnyValidSequenceLandsInReachable(t *testing.T) {
	reachable := buildReachableSet(applications.StatusNew)

	rapid.Check(t, func(rt *rapid.T) {
		cur := applications.StatusNew
		// Walk up to 20 steps, picking a random allowed next.
		for step := 0; step < 20; step++ {
			next := applications.AllowedNext(cur)
			if len(next) == 0 {
				break
			}
			chosen := next[rapid.IntRange(0, len(next)-1).Draw(rt, "pick")]
			if err := applications.ValidateTransition(cur, chosen); err != nil {
				rt.Fatalf("transition %s → %s reported invalid by validator", cur, chosen)
			}
			cur = chosen
		}
		if !reachable[cur] {
			rt.Fatalf("walked into %s, which is not in the reachable set from new", cur)
		}
	})
}

func buildReachableSet(from applications.Status) map[applications.Status]bool {
	seen := map[applications.Status]bool{from: true}
	queue := []applications.Status{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range applications.AllowedNext(cur) {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return seen
}
