package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

type candidateKey struct{}

// CandidateAuth pulls the candidate identity from `X-Candidate-ID`.
// Missing or empty → 401 problem+json. The Phase-3 plan accepts header-
// based identity; OIDC bearer validation is layered on later.
func CandidateAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Candidate-ID")
		if id == "" {
			ProblemJSON(w, http.StatusUnauthorized,
				"unauthorized", "X-Candidate-ID header required")
			return
		}
		ctx := context.WithValue(r.Context(), candidateKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CandidateFromContext returns the authenticated candidate ID. Panics
// if called from a route that wasn't wrapped in CandidateAuth.
func CandidateFromContext(ctx context.Context) string {
	v, _ := ctx.Value(candidateKey{}).(string)
	if v == "" {
		panic("applications: CandidateFromContext called outside CandidateAuth")
	}
	return v
}

// IdempotencyConfig configures the middleware.
type IdempotencyConfig struct {
	Store      *applications.IdempotencyStore
	RouteGroup string
}

// Idempotency wraps a mutating handler. Behaviour:
//   - If header `Idempotency-Key` is missing → pass through (no replay).
//   - If found and the store has a saved row → return the saved
//     response bytes/status/headers directly.
//   - Otherwise: capture the response. On success (2xx), save it under
//     the key with the configured TTL. Replay safe for the configured
//     window.
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
			ProblemJSON(w, http.StatusInternalServerError,
				"internal_error", "idempotency lookup failed")
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

		// Only persist 2xx responses (idempotency caches success).
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
			_ = cfg.Store.Save(r.Context(), candidate, key, cfg.RouteGroup,
				applications.StoredResponse{
					StatusCode: rec.status,
					Body:       body,
					Headers:    headers,
				})
		}
	})
}

// recorder is a ResponseWriter that captures status + body in addition
// to writing to the underlying writer. We need to capture the body so
// the same bytes can be replayed on idempotent retries.
type recorder struct {
	http.ResponseWriter
	header  http.Header
	status  int
	written bool
	buf     bytes.Buffer
}

func (r *recorder) Header() http.Header {
	if r.written {
		// After WriteHeader, additional Header() writes still go to the
		// underlying writer (Go's http package quirk), but the snapshot
		// we save is the headers as of WriteHeader.
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

// Problem is the RFC 9457 envelope.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// ProblemJSON writes an application/problem+json error response.
func ProblemJSON(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type: "about:blank", Title: title, Status: status, Detail: detail,
	})
}

// ProblemFromError maps known sentinel errors to standard problems.
// Unknown errors return 500 with a generic title.
func ProblemFromError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, applications.ErrNotFound):
		ProblemJSON(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, applications.ErrAlreadyExists):
		ProblemJSON(w, http.StatusConflict, "already_exists", err.Error())
	default:
		var ie *applications.InvalidTransitionError
		if errors.As(err, &ie) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type":    "about:blank",
				"title":   "invalid_transition",
				"status":  http.StatusConflict,
				"detail":  err.Error(),
				"from":    string(ie.From),
				"to":      string(ie.To),
				"allowed": ie.Allowed,
			})
			return
		}
		ProblemJSON(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
