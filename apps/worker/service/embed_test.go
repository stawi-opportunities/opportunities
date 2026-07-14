package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/apps/worker/service"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type fakeEmbedder struct {
	vec []float32
	err error
}

func (f *fakeEmbedder) Embed(context.Context, string) ([]float32, error) {
	return f.vec, f.err
}

type fakeEmbedStore struct {
	id  string
	vec []float32
	err error
}

func (f *fakeEmbedStore) SetEmbedding(_ context.Context, id string, vec []float32) error {
	f.id = id
	f.vec = vec
	return f.err
}

func TestEmbedHandler_WritesVector(t *testing.T) {
	t.Parallel()
	store := &fakeEmbedStore{}
	h := service.NewEmbedHandler(store, &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}})
	env := eventsv1.NewEnvelope(eventsv1.SubjectWorkerEmbed, eventsv1.OpportunityEmbedV1{
		OpportunityID: "opp1",
		Title:         "Engineer",
		IssuingEntity: "Acme",
		Description:   "Build things",
	})
	body, err := json.Marshal(env)
	require.NoError(t, err)
	require.NoError(t, h.Handle(context.Background(), nil, body))
	require.Equal(t, "opp1", store.id)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, store.vec)
}

func TestEmbedHandler_RetriesOnProviderError(t *testing.T) {
	t.Parallel()
	h := service.NewEmbedHandler(&fakeEmbedStore{}, &fakeEmbedder{err: errors.New("rate limit")})
	env := eventsv1.NewEnvelope(eventsv1.SubjectWorkerEmbed, eventsv1.OpportunityEmbedV1{
		OpportunityID: "opp1", Title: "T",
	})
	body, err := json.Marshal(env)
	require.NoError(t, err)
	require.Error(t, h.Handle(context.Background(), nil, body))
}
