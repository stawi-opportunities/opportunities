package v1_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/matching/v1"
)

type fakePublisher struct {
	published [][]byte
	subject   string
}

func (f *fakePublisher) Publish(_ context.Context, subject string, payload []byte) error {
	f.subject = subject
	f.published = append(f.published, payload)
	return nil
}

func TestDLQGuard_BelowThreshold_PropagatesError(t *testing.T) {
	pub := &fakePublisher{}
	g := v1.NewDLQGuard(pub, "svc.opportunities.matching.deadletter", 5)

	err := g.Run(context.Background(), 3, []byte("payload"), func() error {
		return errors.New("transient")
	})
	require.Error(t, err)
	require.Empty(t, pub.published)
}

func TestDLQGuard_AtThreshold_PublishesToDLQ_ReturnsNil(t *testing.T) {
	pub := &fakePublisher{}
	g := v1.NewDLQGuard(pub, "svc.opportunities.matching.deadletter", 5)

	err := g.Run(context.Background(), 5, []byte("payload"), func() error {
		return errors.New("poison")
	})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("payload")}, pub.published)
	require.Equal(t, "svc.opportunities.matching.deadletter", pub.subject)
}

func TestDLQGuard_Success_PassesThrough(t *testing.T) {
	pub := &fakePublisher{}
	g := v1.NewDLQGuard(pub, "svc.opportunities.matching.deadletter", 5)

	err := g.Run(context.Background(), 2, []byte("payload"), func() error { return nil })
	require.NoError(t, err)
	require.Empty(t, pub.published)
}
