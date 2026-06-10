package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRecipeStore struct {
	activated *recipe.Recipe
	passRate  float64
}

func (f *fakeRecipeStore) Activate(_ context.Context, _ string, rec *recipe.Recipe, passRate float64, _ string, _ any) error {
	f.activated = rec
	f.passRate = passRate
	return nil
}

type fakeSourceByID struct{ src domain.Source }

func (f fakeSourceByID) GetByID(_ context.Context, _ string) (*domain.Source, error) {
	s := f.src
	return &s, nil
}

type fakeGenLLM struct {
	reply string
	err   error
}

func (f fakeGenLLM) Complete(_ context.Context, _ string) (string, error) { return f.reply, f.err }

type headeredClient struct{ c *http.Client }

func (h headeredClient) Get(ctx context.Context, url string, _ map[string]string) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := h.c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var b []byte
	tmp := make([]byte, 2048)
	for {
		n, rerr := resp.Body.Read(tmp)
		b = append(b, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return b, resp.StatusCode, nil
}

func TestRecipeGenerateHandler_GeneratesValidatesActivates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><script type="application/ld+json">{"title":"Go Eng","description":"A description well over the fifty character verify minimum for sure now.","hiringOrganization":{"name":"ACME"},"url":"https://x.io/apply","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`))
	}))
	defer srv.Close()

	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Skipf("kinds: %v", err)
	}

	recipeJSON := `{"acquisition":"structured_data","kind":{"mode":"source_default"},"list":{"mode":"selector","item_selector":".c","link":{"from":["selector"],"selector":"a","attr":"href"},"pagination":{"mode":"none"}},"detail":{"record_source":"json_ld","title":{"from":["json_ld"],"json_path":"$.title"},"description":{"from":["json_ld"],"json_path":"$.description"},"issuing_entity":{"from":["json_ld"],"json_path":"$.hiringOrganization.name"},"apply_url":{"from":["json_ld"],"json_path":"$.url"},"anchor_country":{"from":["json_ld"],"json_path":"$.jobLocation.address.addressCountry"}}}`

	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "KE"}
	src.ID = "s1"
	src.Kinds = []string{"job"}

	store := &fakeRecipeStore{}
	fetcher := recipe.NewHTTPFetcher(headeredClient{c: srv.Client()})
	gen := recipe.NewGenerator(fakeGenLLM{reply: recipeJSON}, fetcher, reg, 3)
	h := NewRecipeGenerateHandler(RecipeHandlerDeps{
		Sources: fakeSourceByID{src: src}, Recipes: store, Generator: gen, Registry: reg,
		Fetcher: fetcher, PassThreshold: 0.8,
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicRecipeGenerate, eventsv1.RecipeGenerateV1{SourceID: "s1", SampleURLs: []string{srv.URL}})
	body, _ := json.Marshal(env)
	raw := json.RawMessage(body)
	require.NoError(t, h.Execute(context.Background(), &raw))

	require.NotNil(t, store.activated)
	assert.Equal(t, "structured_data", store.activated.Acquisition)
	assert.GreaterOrEqual(t, store.passRate, 0.8)
}

type fakeFlagger struct{ flagged bool }

func (f *fakeFlagger) FlagNeedsTuning(_ context.Context, _ string, v bool) error {
	f.flagged = v
	return nil
}

func TestRecipeGenerateHandler_BelowThresholdFlagsAndAcks(t *testing.T) {
	// Sample page has NO structured data, so the recipe extracts nothing → pass-rate 0.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>no structured data</body></html>`))
	}))
	defer srv.Close()
	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Skipf("kinds: %v", err)
	}
	recipeJSON := `{"acquisition":"structured_data","kind":{"mode":"source_default"},"list":{"mode":"selector","item_selector":".c","link":{"from":["selector"],"selector":"a","attr":"href"},"pagination":{"mode":"none"}},"detail":{"record_source":"json_ld","title":{"from":["json_ld"],"json_path":"$.title"},"description":{"from":["json_ld"],"json_path":"$.description"},"issuing_entity":{"from":["json_ld"],"json_path":"$.c"},"apply_url":{"from":["json_ld"],"json_path":"$.u"},"anchor_country":{"from":["json_ld"],"json_path":"$.k"}}}`
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "KE"}
	src.ID = "s2"
	src.Kinds = []string{"job"}
	store := &fakeRecipeStore{}
	flagger := &fakeFlagger{}
	fetcher := recipe.NewHTTPFetcher(headeredClient{c: srv.Client()})
	gen := recipe.NewGenerator(fakeGenLLM{reply: recipeJSON}, fetcher, reg, 3)
	h := NewRecipeGenerateHandler(RecipeHandlerDeps{
		Sources: fakeSourceByID{src: src}, Recipes: store, Generator: gen, Registry: reg,
		Fetcher: fetcher, Flagger: flagger, PassThreshold: 0.8,
	})
	env := eventsv1.NewEnvelope(eventsv1.TopicRecipeGenerate, eventsv1.RecipeGenerateV1{SourceID: "s2", SampleURLs: []string{srv.URL}})
	body, _ := json.Marshal(env)
	raw := json.RawMessage(body)
	require.NoError(t, h.Execute(context.Background(), &raw)) // ACKs (no error)
	assert.Nil(t, store.activated)                            // did not activate
	assert.True(t, flagger.flagged)                           // flagged for operator
}

func TestRecipeGenerateHandler_SynthesisFailureFlagsAndAcks(t *testing.T) {
	// Sample URL returns 500 → Generator yields "no fetchable samples" → the
	// handler must flag needs_tuning and ACK (no error → no Frame redelivery).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Skipf("kinds: %v", err)
	}
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "KE"}
	src.ID = "s3"
	src.Kinds = []string{"job"}
	store := &fakeRecipeStore{}
	flagger := &fakeFlagger{}
	fetcher := recipe.NewHTTPFetcher(headeredClient{c: srv.Client()})
	gen := recipe.NewGenerator(fakeGenLLM{reply: "{}"}, fetcher, reg, 3)
	h := NewRecipeGenerateHandler(RecipeHandlerDeps{
		Sources: fakeSourceByID{src: src}, Recipes: store, Generator: gen, Registry: reg,
		Fetcher: fetcher, Flagger: flagger, PassThreshold: 0.8,
	})
	env := eventsv1.NewEnvelope(eventsv1.TopicRecipeGenerate, eventsv1.RecipeGenerateV1{SourceID: "s3", SampleURLs: []string{srv.URL}})
	body, _ := json.Marshal(env)
	raw := json.RawMessage(body)
	require.NoError(t, h.Execute(context.Background(), &raw)) // ACK, no redelivery
	assert.Nil(t, store.activated)
	assert.True(t, flagger.flagged)
}

func TestRecipeGenerateHandler_RateLimitDoesNotFlag(t *testing.T) {
	// A 429-style failure must NOT flag needs_tuning — the source stays queued
	// for a later tick instead of draining into quarantine.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>job page</body></html>"))
	}))
	defer srv.Close()
	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Skipf("kinds: %v", err)
	}
	src := domain.Source{Type: "brightermonday", BaseURL: srv.URL, Country: "KE"}
	src.ID = "s4"
	src.Kinds = []string{"job"}
	store := &fakeRecipeStore{}
	flagger := &fakeFlagger{}
	fetcher := recipe.NewHTTPFetcher(headeredClient{c: srv.Client()})
	gen := recipe.NewGenerator(fakeGenLLM{err: fmt.Errorf("chat: rate-limited after 4 attempts: chat: status 429: TPM limit reached")}, fetcher, reg, 3)
	h := NewRecipeGenerateHandler(RecipeHandlerDeps{
		Sources: fakeSourceByID{src: src}, Recipes: store, Generator: gen, Registry: reg,
		Fetcher: fetcher, Flagger: flagger, PassThreshold: 0.8,
	})
	env := eventsv1.NewEnvelope(eventsv1.TopicRecipeGenerate, eventsv1.RecipeGenerateV1{SourceID: "s4", SampleURLs: []string{srv.URL}})
	body, _ := json.Marshal(env)
	raw := json.RawMessage(body)
	require.NoError(t, h.Execute(context.Background(), &raw))
	assert.Nil(t, store.activated)
	assert.False(t, flagger.flagged, "rate-limited source must stay queued, not flagged")
}
