package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// ActiveCandidateLister enumerates the candidate IDs the weekly match
// digest refreshes. Production impl wraps
// CandidateRepository.ListActive.
type ActiveCandidateLister interface {
	ListActive(ctx context.Context) ([]string, error)
}

// CandidateIndexReader loads a candidate's match index (embedding +
// per-kind / geo / salary prefs). *matching.IndexStore satisfies this.
type CandidateIndexReader interface {
	Get(ctx context.Context, candidateID string) (*matching.CandidateIndex, error)
}

// MatchesWeeklyDigestDeps bundles the collaborators for the weekly
// match-digest sweep. KNN / Store / EventLog / Reranker / Weights are the
// same pkg/matching machinery the candidate-change consumer uses for
// Path C gap-fill, so the weekly sweep produces identical matches.
type MatchesWeeklyDigestDeps struct {
	Svc      *frame.Service
	Active   ActiveCandidateLister
	Index    CandidateIndexReader
	KNN      *matching.KNN
	Store    *matching.Store
	EventLog *matching.EventLog
	Reranker matching.Reranker
	Weights  matching.Weights
	// Since bounds the gap-fill look-back window. Defaults to 30 days.
	Since time.Duration
}

type matchesWeeklyDigestResponse struct {
	OK       bool `json:"ok"`
	Audience int  `json:"audience"`
	Matched  int  `json:"matched"`
	Skipped  int  `json:"skipped"`
	Failed   int  `json:"failed"`
}

// MatchesWeeklyDigestHandler serves POST /_admin/matches/weekly_digest —
// the Monday-morning Trustage cron. For every ACTIVE candidate it runs
// the gap-fill match pipeline (reusing pkg/matching) so the candidate's
// candidate_matches are refreshed, then emits one
// candidates.matches.ready.v1 envelope per candidate for the downstream
// notification service to deliver.
//
// Per-candidate failures are logged + counted but do NOT abort the
// sweep. Candidates without an embedding/index row are skipped — there's
// nothing to match against until a CV (or preferences) lands.
func MatchesWeeklyDigestHandler(deps MatchesWeeklyDigestDeps) http.HandlerFunc {
	since := deps.Since
	if since <= 0 {
		since = 30 * 24 * time.Hour
	}
	weights := deps.Weights
	if weights == (matching.Weights{}) {
		weights = matching.DefaultWeights()
	}
	reranker := deps.Reranker
	if reranker == nil {
		reranker = matching.NoopReranker{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		ids, err := deps.Active.ListActive(ctx)
		if err != nil {
			log.WithError(err).Error("matches-weekly-digest: ListActive failed")
			http.Error(w, `{"error":"list active failed"}`, http.StatusInternalServerError)
			return
		}

		gapDeps := matching.GapFillDeps{
			KNN:      deps.KNN,
			Store:    deps.Store,
			EventLog: deps.EventLog,
			Reranker: reranker,
			Weights:  weights,
		}
		cutoff := time.Now().Add(-since)

		resp := matchesWeeklyDigestResponse{OK: true, Audience: len(ids)}
		for _, id := range ids {
			idx, idxErr := deps.Index.Get(ctx, id)
			if errors.Is(idxErr, matching.ErrNotFound) || (idxErr == nil && len(idx.Embedding) == 0) {
				// No embedding yet (CV not processed / no preferences) —
				// nothing to match against. Skip rather than fail.
				resp.Skipped++
				continue
			}
			if idxErr != nil {
				log.WithError(idxErr).WithField("candidate_id", id).
					Warn("matches-weekly-digest: index lookup failed")
				resp.Failed++
				continue
			}

			res, runErr := matching.GapFill(ctx, matching.GapFillInput{
				CandidateID:    id,
				Embedding:      idx.Embedding,
				Countries:      idx.Countries,
				Kinds:          idx.Kinds,
				SalaryFloorUSD: idx.SalaryFloorUSD,
				Since:          cutoff,
				MinScore:       idx.MinScore,
			}, gapDeps)
			if runErr != nil {
				log.WithError(runErr).WithField("candidate_id", id).
					Warn("matches-weekly-digest: gap-fill failed")
				resp.Failed++
				continue
			}

			env := eventsv1.NewEnvelope(
				eventsv1.TopicCandidateMatchesReady,
				eventsv1.MatchesReadyV1{
					CandidateID:  id,
					MatchBatchID: res.RunID,
				},
			)
			if emitErr := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); emitErr != nil {
				log.WithError(emitErr).WithField("candidate_id", id).
					Warn("matches-weekly-digest: emit MatchesReadyV1 failed")
				resp.Failed++
				continue
			}
			resp.Matched++
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ── Production adapter ──────────────────────────────────────────────

// RepoActiveCandidateLister adapts the repository into
// ActiveCandidateLister, projecting to bare candidate IDs and capping the
// per-sweep audience.
type RepoActiveCandidateLister struct {
	list  func(ctx context.Context, limit int) ([]string, error)
	limit int
}

// NewRepoActiveCandidateLister wires the adapter from any function that
// lists active candidate IDs (in production a thin closure over
// CandidateRepository.ListActive). `limit` caps the audience so a runaway
// query can't OOM the matching pod.
func NewRepoActiveCandidateLister(list func(ctx context.Context, limit int) ([]string, error), limit int) *RepoActiveCandidateLister {
	if limit <= 0 {
		limit = 5000
	}
	return &RepoActiveCandidateLister{list: list, limit: limit}
}

func (l *RepoActiveCandidateLister) ListActive(ctx context.Context) ([]string, error) {
	return l.list(ctx, l.limit)
}
