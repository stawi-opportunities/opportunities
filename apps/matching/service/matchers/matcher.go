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
