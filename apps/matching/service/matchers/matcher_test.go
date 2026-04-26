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

func (s *stubMatcher) Kind() string                                        { return s.kind }
func (s *stubMatcher) SearchFilter(_ json.RawMessage) (any, error)         { return nil, nil }
func (s *stubMatcher) Score(_ context.Context, _ json.RawMessage, _ any) (float64, error) {
	return 0, nil
}
