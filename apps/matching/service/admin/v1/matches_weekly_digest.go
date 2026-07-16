package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// DigestAudienceMember is one entitled subscriber considered for a match digest.
type DigestAudienceMember struct {
	ID            string
	EmailDigest   string
	WeeklySummary bool
	CommEmail     bool
}

// ActiveCandidateLister enumerates the candidate IDs (and digest prefs)
// the match-digest sweep considers. Production wraps ListPaidActive.
type ActiveCandidateLister interface {
	ListActive(ctx context.Context) ([]DigestAudienceMember, error)
}

// CandidateIndexReader loads a candidate's match index (embedding +
// per-kind / geo / salary prefs). *matching.IndexStore satisfies this.
type CandidateIndexReader interface {
	Get(ctx context.Context, candidateID string) (*matching.CandidateIndex, error)
}

// DigestTouch records last_digest_at after a successful emit. Optional.
type DigestTouch interface {
	TouchLastDigestAt(ctx context.Context, candidateID string, at time.Time) error
}

// MatchesWeeklyDigestDeps bundles the collaborators for the match-digest
// sweep. KNN / Store / EventLog / Reranker / Weights are the same
// pkg/matching machinery Path C uses so digests produce identical matches.
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
	// DefaultMinScore floors gap-fill when the index has no per-candidate
	// threshold (MATCHING_MIN_SCORE). 0 → 0.45.
	DefaultMinScore float64
	// DefaultCadence is used when the request body omits cadence.
	// "auto" (default) honours each user's email_digest + WeeklyWeekday.
	DefaultCadence string
	// WeeklyWeekday is the local weekday for weekly digests under auto mode.
	// Default Monday.
	WeeklyWeekday time.Weekday
	// Location is the timezone for weekly weekday evaluation. Default UTC.
	Location *time.Location
	// Toucher optional — stamps last_digest_at after emit.
	Toucher DigestTouch
	// Now injectable for tests.
	Now func() time.Time
}

func digestMinScore(indexScore, defaultScore float64) float64 {
	if indexScore > 0 && indexScore <= 1 {
		return indexScore
	}
	if defaultScore > 0 && defaultScore <= 1 {
		return defaultScore
	}
	return 0.45
}

type digestRunRequest struct {
	// Cadence: auto | daily | weekly. Empty → DefaultCadence or auto.
	Cadence string `json:"cadence"`
}

type matchesWeeklyDigestResponse struct {
	OK       bool   `json:"ok"`
	Cadence  string `json:"cadence"`
	Audience int    `json:"audience"`
	Matched  int    `json:"matched"`
	Skipped  int    `json:"skipped"`
	Failed   int    `json:"failed"`
}

// MatchesWeeklyDigestHandler serves POST /_admin/matches/weekly_digest —
// Trustage (or ops) invokes this on a configurable cron. For each entitled
// subscriber whose notification prefs accept the run cadence it gap-fills
// matches above the quality threshold and emits candidates.matches.ready.v1
// for the notification service summary email.
//
// Request body (optional):
//
//	{"cadence":"auto"|"daily"|"weekly"}
//
// auto (default) sends daily users every run and weekly users only on
// WeeklyWeekday in Location.
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
	loc := deps.Location
	if loc == nil {
		loc = time.UTC
	}
	weeklyDOW := deps.WeeklyWeekday
	// zero value is Sunday; treat unset as Monday when never configured.
	// Callers should set explicitly; main always does.
	nowFn := deps.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	defaultCadence := strings.ToLower(strings.TrimSpace(deps.DefaultCadence))
	if defaultCadence == "" {
		defaultCadence = "auto"
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		cadence := defaultCadence
		if r.Body != nil {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if len(raw) > 0 {
				var req digestRunRequest
				if err := json.Unmarshal(raw, &req); err == nil && strings.TrimSpace(req.Cadence) != "" {
					cadence = strings.ToLower(strings.TrimSpace(req.Cadence))
				}
			}
		}

		members, err := deps.Active.ListActive(ctx)
		if err != nil {
			log.WithError(err).Error("matches-digest: ListActive failed")
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
		now := nowFn().UTC()
		cutoff := now.Add(-since)

		resp := matchesWeeklyDigestResponse{OK: true, Cadence: cadence, Audience: len(members)}
		for _, m := range members {
			prefs := matching.DigestPrefs{
				EmailDigest:   m.EmailDigest,
				WeeklySummary: m.WeeklySummary,
				CommEmail:     m.CommEmail,
			}
			// Empty CommEmail column default: treat missing as true when zero-value from older rows.
			// GORM default is true; zero-value false only when explicitly set.
			if !matching.ShouldSendDigest(prefs, cadence, now, loc, weeklyDOW) {
				resp.Skipped++
				continue
			}

			idx, idxErr := deps.Index.Get(ctx, m.ID)
			if errors.Is(idxErr, matching.ErrNotFound) || (idxErr == nil && (idx == nil || len(idx.Embedding) == 0)) {
				resp.Skipped++
				continue
			}
			if idxErr != nil {
				log.WithError(idxErr).WithField("candidate_id", m.ID).
					Warn("matches-digest: index lookup failed")
				resp.Failed++
				continue
			}

			res, runErr := matching.GapFill(ctx, matching.GapFillInput{
				CandidateID:    m.ID,
				Embedding:      idx.Embedding,
				Countries:      idx.Countries,
				Kinds:          idx.Kinds,
				SalaryFloorUSD: idx.SalaryFloorUSD,
				Since:          cutoff,
				MinScore:       digestMinScore(idx.MinScore, deps.DefaultMinScore),
			}, gapDeps)
			if runErr != nil {
				log.WithError(runErr).WithField("candidate_id", m.ID).
					Warn("matches-digest: gap-fill failed")
				resp.Failed++
				continue
			}

			env := eventsv1.NewEnvelope(
				eventsv1.TopicCandidateMatchesReady,
				eventsv1.MatchesReadyV1{
					CandidateID:  m.ID,
					MatchBatchID: res.RunID,
				},
			)
			if emitErr := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); emitErr != nil {
				log.WithError(emitErr).WithField("candidate_id", m.ID).
					Warn("matches-digest: emit MatchesReadyV1 failed")
				resp.Failed++
				continue
			}
			if deps.Toucher != nil {
				if tErr := deps.Toucher.TouchLastDigestAt(ctx, m.ID, now); tErr != nil {
					log.WithError(tErr).WithField("candidate_id", m.ID).
						Debug("matches-digest: touch last_digest_at failed")
				}
			}
			resp.Matched++
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ── Production adapter ──────────────────────────────────────────────

// RepoActiveCandidateLister adapts a list of profiles into DigestAudienceMembers.
type RepoActiveCandidateLister struct {
	list  func(ctx context.Context, limit int) ([]DigestAudienceMember, error)
	limit int
}

// NewRepoActiveCandidateLister wires the adapter. `limit` caps audience size.
func NewRepoActiveCandidateLister(list func(ctx context.Context, limit int) ([]DigestAudienceMember, error), limit int) *RepoActiveCandidateLister {
	if limit <= 0 {
		limit = 5000
	}
	return &RepoActiveCandidateLister{list: list, limit: limit}
}

func (l *RepoActiveCandidateLister) ListActive(ctx context.Context) ([]DigestAudienceMember, error) {
	return l.list(ctx, l.limit)
}
