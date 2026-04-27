package matchers

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistry_Register_Lookup(t *testing.T) {
	r := NewRegistry()
	stub := &stubMatcher{kind: "job"}
	r.Register(stub)
	got, ok := r.For("job")
	if !ok || got != stub {
		t.Fatalf("For(job) = %v,%v, want stub,true", got, ok)
	}
	if _, ok := r.For("unknown"); ok {
		t.Fatal("For(unknown) should report missing")
	}
}

type stubMatcher struct{ kind string }

func (s *stubMatcher) Kind() string                                { return s.kind }
func (s *stubMatcher) Disabled() bool                              { return false }
func (s *stubMatcher) SearchFilter(_ json.RawMessage) (any, error) { return nil, nil }
func (s *stubMatcher) Score(_ context.Context, _ json.RawMessage, _ any) (float64, error) {
	return 0, nil
}

func TestMatcher_Disabled(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubEnabledMatcher{kind: "job"})
	r.Register(&stubDisabledMatcher{kind: "tender"})
	enabled := r.EnabledKinds()
	if len(enabled) != 1 || enabled[0] != "job" {
		t.Fatalf("EnabledKinds = %v, want [job]", enabled)
	}
}

type stubEnabledMatcher struct{ kind string }

func (s *stubEnabledMatcher) Kind() string                                { return s.kind }
func (s *stubEnabledMatcher) Disabled() bool                              { return false }
func (s *stubEnabledMatcher) SearchFilter(_ json.RawMessage) (any, error) { return nil, nil }
func (s *stubEnabledMatcher) Score(_ context.Context, _ json.RawMessage, _ any) (float64, error) {
	return 0, nil
}

type stubDisabledMatcher struct{ kind string }

func (s *stubDisabledMatcher) Kind() string                                { return s.kind }
func (s *stubDisabledMatcher) Disabled() bool                              { return true }
func (s *stubDisabledMatcher) SearchFilter(_ json.RawMessage) (any, error) { return nil, nil }
func (s *stubDisabledMatcher) Score(_ context.Context, _ json.RawMessage, _ any) (float64, error) {
	return 0, nil
}
