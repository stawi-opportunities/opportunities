package autoapply

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

type stubSubmitter struct {
	name      string
	canHandle bool
	result    SubmitResult
	err       error
	called    int
}

func (s *stubSubmitter) Name() string                         { return s.name }
func (s *stubSubmitter) CanHandle(_ domain.SourceType, _ string) bool { return s.canHandle }
func (s *stubSubmitter) Submit(_ context.Context, _ SubmitRequest) (SubmitResult, error) {
	s.called++
	return s.result, s.err
}

func TestRegistry_FirstMatchWins(t *testing.T) {
	a := &stubSubmitter{name: "a", canHandle: false}
	b := &stubSubmitter{name: "b", canHandle: true, result: SubmitResult{Method: "b"}}
	c := &stubSubmitter{name: "c", canHandle: true, result: SubmitResult{Method: "c"}}
	reg := NewRegistry(a, b, c)

	res, err := reg.Submit(context.Background(), SubmitRequest{ApplyURL: "x"})
	require.NoError(t, err)
	assert.Equal(t, "b", res.Method)
	assert.Equal(t, 0, a.called)
	assert.Equal(t, 1, b.called)
	assert.Equal(t, 0, c.called, "later tier must not run when earlier matched")
}

func TestRegistry_NoMatchSkips(t *testing.T) {
	a := &stubSubmitter{name: "a", canHandle: false}
	reg := NewRegistry(a)
	res, err := reg.Submit(context.Background(), SubmitRequest{ApplyURL: "x"})
	require.NoError(t, err)
	assert.Equal(t, "skipped", res.Method)
	assert.Equal(t, "no_submitter", res.SkipReason)
}

func TestRegistry_ErrorPropagates(t *testing.T) {
	a := &stubSubmitter{name: "a", canHandle: true, err: errors.New("boom")}
	reg := NewRegistry(a)
	_, err := reg.Submit(context.Background(), SubmitRequest{ApplyURL: "x"})
	require.Error(t, err)
}
