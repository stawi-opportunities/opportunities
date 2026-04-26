package matchers

import (
	"context"
	"encoding/json"
)

// Matcher is the contract every kind-specific matcher implements.
// Registered with the matchers Registry at app boot; the router selects
// by Kind().
type Matcher interface {
	Kind() string
	// Disabled reports whether this matcher is a stub not yet ready for
	// production. Disabled matchers stay registered (so the router can
	// answer "we know about this kind") but are filtered out of the
	// EnabledKinds list the UI uses to gate onboarding.
	Disabled() bool
	// SearchFilter returns a kind-scoped Manticore filter expression
	// (or equivalent) built from the candidate's preferences blob.
	SearchFilter(prefs json.RawMessage) (any, error)
	// Score ranks one opportunity against the same preferences blob.
	// Returns a value in [0, 1].
	Score(ctx context.Context, prefs json.RawMessage, opp any) (float64, error)
}

type Registry struct{ m map[string]Matcher }

func NewRegistry() *Registry { return &Registry{m: map[string]Matcher{}} }

func (r *Registry) Register(mt Matcher) { r.m[mt.Kind()] = mt }

func (r *Registry) For(kind string) (Matcher, bool) {
	mt, ok := r.m[kind]
	return mt, ok
}

func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	return out
}

// EnabledKinds returns the kinds whose matcher is not disabled. Used by
// the /v1/match-kinds endpoint to tell the UI which onboarding tabs to
// render — stubs (Disabled() == true) are excluded so candidates can't
// opt into a kind whose matcher returns uniform 0.5 scores.
func (r *Registry) EnabledKinds() []string {
	out := make([]string, 0, len(r.m))
	for k, mt := range r.m {
		if !mt.Disabled() {
			out = append(out, k)
		}
	}
	return out
}
