package httpmw

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// IdempotencyStore is the contract the idempotency middleware needs.
// The pg-backed applications.IdempotencyStore satisfies this signature
// via the StoredResponse type alias in pkg/applications.
type IdempotencyStore interface {
	Lookup(ctx context.Context, candidateID, key, routeGroup string) (*StoredResponse, error)
	Save(ctx context.Context, candidateID, key, routeGroup string, resp StoredResponse) error
}

// StoredResponse mirrors the shape the IdempotencyStore persists.
type StoredResponse struct {
	StatusCode int               `json:"status"`
	Body       json.RawMessage   `json:"body"`
	Headers    map[string]string `json:"headers"`
}

// IdempotencyConfig configures the middleware.
type IdempotencyConfig struct {
	Store      IdempotencyStore
	RouteGroup string
}

// Idempotency wraps a mutating handler. Behaviour:
//   - If header Idempotency-Key is missing → pass through (no replay).
//   - If found and the store has a saved row → return the saved
//     response bytes/status/headers directly.
//   - Otherwise: capture the response. On success (2xx), save it under
//     the key. Replay safe for the configured window.
func Idempotency(cfg IdempotencyConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		candidate := CandidateFromContext(r.Context())

		prior, err := cfg.Store.Lookup(r.Context(), candidate, key, cfg.RouteGroup)
		if err != nil {
			ProblemJSON(w, http.StatusInternalServerError, "internal_error", "idempotency lookup failed")
			return
		}
		if prior != nil {
			for k, v := range prior.Headers {
				w.Header().Set(k, v)
			}
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(prior.StatusCode)
			_, _ = w.Write(prior.Body)
			return
		}

		rec := &recorder{ResponseWriter: w, header: http.Header{}, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		if rec.status >= 200 && rec.status < 300 {
			headers := map[string]string{}
			for k, vs := range rec.header {
				if len(vs) > 0 {
					headers[k] = vs[0]
				}
			}
			body := rec.buf.Bytes()
			if len(body) == 0 {
				body = []byte("null")
			}
			_ = cfg.Store.Save(r.Context(), candidate, key, cfg.RouteGroup, StoredResponse{
				StatusCode: rec.status,
				Body:       body,
				Headers:    headers,
			})
		}
	})
}

type recorder struct {
	http.ResponseWriter
	header  http.Header
	status  int
	written bool
	buf     bytes.Buffer
}

func (r *recorder) Header() http.Header {
	if r.written {
		return r.ResponseWriter.Header()
	}
	return r.header
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	for k, vs := range r.header {
		for _, v := range vs {
			r.ResponseWriter.Header().Add(k, v)
		}
	}
	r.ResponseWriter.WriteHeader(status)
	r.written = true
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}
