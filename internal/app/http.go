package app

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"stawi.jobs/internal/domain"
	"stawi.jobs/internal/indexer"
)

func NewSearchRouter(store domain.JobStore, idx indexer.Indexer) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/search", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query().Get("q")
		limit := parseInt(req.URL.Query().Get("limit"), 20)
		jobs, err := idx.Search(req.Context(), q, limit)
		if err != nil {
			jobs, err = store.SearchCanonicalJobs(req.Context(), q, limit)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(jobs), "jobs": jobs})
	})
	return r
}

func NewOpsRouter(store domain.SourceStore) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/sources", func(w http.ResponseWriter, req *http.Request) {
		limit := parseInt(req.URL.Query().Get("limit"), 200)
		sources, err := store.ListSources(req.Context(), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(sources), "sources": sources})
	})
	return r
}

func parseInt(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
