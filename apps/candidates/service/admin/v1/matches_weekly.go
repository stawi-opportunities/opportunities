// Package v1 contains the Phase 5 Trustage admin HTTP handlers for
// apps/candidates: matches weekly digest + CV stale nudge.
package v1

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pitabwire/util"
)

// CandidateLister enumerates active candidates (the universe targeted
// by the weekly match digest). In production the implementation reads
// Postgres `candidates` WHERE status='active'; v1.1 swaps in an
// event-sourced candidate ledger.
type CandidateLister interface {
	ListActive(ctx context.Context) ([]string, error)
}

// MatchRunner runs the match pipeline for one candidate. In production
// the implementation calls the match HTTP handler's internals directly
// (or via an in-process function pointer); tests use a fake.
type MatchRunner interface {
	RunMatch(ctx context.Context, candidateID string) error
}

// MatchesWeeklyDeps bundles collaborators.
type MatchesWeeklyDeps struct {
	Lister CandidateLister
	Runner MatchRunner
}

type matchesWeeklyResponse struct {
	OK        bool `json:"ok"`
	Processed int  `json:"processed"`
	Failed    int  `json:"failed"`
}

// MatchesWeeklyHandler returns an http.HandlerFunc fired by Trustage on
// a weekly cron. It enumerates active candidates and invokes RunMatch
// for each one sequentially. Failures per-candidate are logged and
// counted; they do not abort the sweep.
func MatchesWeeklyHandler(deps MatchesWeeklyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		ids, err := deps.Lister.ListActive(ctx)
		if err != nil {
			http.Error(w, `{"error":"list active failed"}`, http.StatusInternalServerError)
			return
		}
		resp := matchesWeeklyResponse{OK: true}
		for _, id := range ids {
			if err := deps.Runner.RunMatch(ctx, id); err != nil {
				log.WithError(err).WithField("candidate_id", id).Warn("weekly digest: RunMatch failed")
				resp.Failed++
				continue
			}
			resp.Processed++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
