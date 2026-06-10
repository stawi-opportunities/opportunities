package recipe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLLM returns a canned response per call, advancing through `replies`.
type fakeLLM struct {
	replies    []string
	calls      int
	lastPrompt string
}

func (f *fakeLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.lastPrompt = prompt
	r := f.replies[min(f.calls, len(f.replies)-1)]
	f.calls++
	return r, nil
}

func minimalRegistry(t *testing.T) *opportunity.Registry {
	t.Helper()
	reg, err := opportunity.LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Skipf("opportunity kinds not loadable: %v", err)
	}
	return reg
}

const validRecipeJSON = `{"acquisition":"structured_data","kind":{"mode":"source_default"},
"list":{"mode":"selector","item_selector":".card","link":{"from":["selector"],"selector":"a","attr":"href","transform":["absolute_url"]},"pagination":{"mode":"none"}},
"detail":{"record_source":"json_ld","title":{"from":["json_ld"],"json_path":"$.title"},"description":{"from":["json_ld"],"json_path":"$.description"},"issuing_entity":{"from":["json_ld"],"json_path":"$.hiringOrganization.name"},"apply_url":{"from":["json_ld"],"json_path":"$.url"},"anchor_country":{"from":["json_ld"],"json_path":"$.jobLocation.address.addressCountry"}}}`

func genSource() domain.Source {
	s := domain.Source{Type: "brightermonday", BaseURL: "https://x.io", Country: "KE"}
	s.ID = "g1"
	s.Kinds = []string{"job"}
	return s
}

func TestGenerator_ProducesValidRecipe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><script type="application/ld+json">{"title":"Engineer","description":"We are hiring an engineer to design, build and operate distributed crawling systems across our African job boards platform.","hiringOrganization":{"name":"Acme"},"url":"https://x.io/job/1","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`))
	}))
	defer srv.Close()

	llm := &fakeLLM{replies: []string{"```json\n" + validRecipeJSON + "\n```"}}
	g := NewGenerator(llm, httptestFetcher{client: srv.Client()}, minimalRegistry(t), 3)

	rec, samples, err := g.Generate(context.Background(), genSource(), []string{srv.URL})
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "structured_data", rec.Acquisition)
	assert.NoError(t, rec.Validate())
	require.Len(t, samples, 1)
	// The prompt must mention the target kind's schema (so generation is kind-aware).
	assert.Contains(t, llm.lastPrompt, "job")
}

func TestGenerator_RepairsAfterInvalidThenValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><script type="application/ld+json">{"title":"Engineer","description":"We are hiring an engineer to design, build and operate distributed crawling systems across our African job boards platform.","hiringOrganization":{"name":"Acme"},"url":"https://x.io/job/1","jobLocation":{"address":{"addressCountry":"KE"}}}</script></head><body></body></html>`))
	}))
	defer srv.Close()

	// First reply is structurally invalid (bad acquisition); second is valid.
	bad := `{"acquisition":"telepathy","kind":{"mode":"source_default"},"list":{"mode":"selector","pagination":{"mode":"none"}},"detail":{}}`
	llm := &fakeLLM{replies: []string{bad, validRecipeJSON}}
	g := NewGenerator(llm, httptestFetcher{client: srv.Client()}, minimalRegistry(t), 3)

	rec, _, err := g.Generate(context.Background(), genSource(), []string{srv.URL})
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, 2, llm.calls) // repaired on the second attempt
}

func TestGenerator_FailsAfterMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()
	llm := &fakeLLM{replies: []string{`{"acquisition":"nope"}`}}
	g := NewGenerator(llm, httptestFetcher{client: srv.Client()}, minimalRegistry(t), 2)
	_, _, err := g.Generate(context.Background(), genSource(), []string{srv.URL})
	require.Error(t, err)
	assert.Equal(t, 2, llm.calls)
}

func TestGenerator_NoFetchableSamples(t *testing.T) {
	llm := &fakeLLM{replies: []string{validRecipeJSON}}
	g := NewGenerator(llm, httptestFetcher{client: http.DefaultClient}, minimalRegistry(t), 2)
	_, _, err := g.Generate(context.Background(), genSource(), []string{"http://127.0.0.1:0/nope"})
	require.Error(t, err)
	assert.Zero(t, llm.calls) // never call the LLM with no samples
}
