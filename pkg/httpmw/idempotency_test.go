package httpmw_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

type memoryIdempotency struct {
	mu  sync.Mutex
	rec map[string]httpmw.StoredResponse
}

func (m *memoryIdempotency) Lookup(_ context.Context, candidate, key, group string) (*httpmw.StoredResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rec[candidate+"|"+key+"|"+group]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (m *memoryIdempotency) Save(_ context.Context, candidate, key, group string, resp httpmw.StoredResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		m.rec = map[string]httpmw.StoredResponse{}
	}
	k := candidate + "|" + key + "|" + group
	if _, ok := m.rec[k]; ok {
		return nil
	}
	m.rec[k] = resp
	return nil
}

func TestIdempotency_ReplayReturnsSameResponse(t *testing.T) {
	store := &memoryIdempotency{}
	mid := httpmw.Idempotency(httpmw.IdempotencyConfig{Store: store, RouteGroup: "g"},
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"v":1}`))
		}))
	wrapped := httpmw.CandidateAuth(mid)

	do := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/anything", nil)
		r.Header.Set("X-Candidate-ID", "c")
		r.Header.Set("Idempotency-Key", "k")
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		return w
	}

	w1 := do()
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := do()
	require.Equal(t, http.StatusCreated, w2.Code)
	require.JSONEq(t, w1.Body.String(), w2.Body.String())

	// And the body is what we wrote (not a stub).
	var got map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &got))
	require.Equal(t, float64(1), got["v"])
}

func TestIdempotency_ErrorResponse_NotCached(t *testing.T) {
	store := &memoryIdempotency{}
	callCount := 0
	mid := httpmw.Idempotency(httpmw.IdempotencyConfig{Store: store, RouteGroup: "g"},
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			callCount++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"err":"bad"}`))
		}))
	wrapped := httpmw.CandidateAuth(mid)

	do := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/anything", nil)
		r.Header.Set("X-Candidate-ID", "c")
		r.Header.Set("Idempotency-Key", "k2")
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		return w
	}

	do()
	do()
	// Both calls should reach the inner handler since 4xx is not cached.
	require.Equal(t, 2, callCount)
}
