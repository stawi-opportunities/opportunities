package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
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

// DigestMatchSource lists top unseen matches and records notification receipts.
// *matching.Store implements this. Unit tests inject fakes.
type DigestMatchSource interface {
	ListTopUnseenMatchesForDigest(ctx context.Context, candidateID, channel string, limit int) ([]matching.DigestMatch, error)
	InsertNotificationReceipts(ctx context.Context, candidateID, channel string, items []matching.DigestMatch) error
}

// MatchesWeeklyDigestDeps bundles the collaborators for the match-digest
// sweep. KNN / Store / EventLog / Reranker / Weights are the same
// pkg/matching machinery Path C uses so digests produce identical matches.
type MatchesWeeklyDigestDeps struct {
	Svc    *frame.Service
	Active ActiveCandidateLister
	Index  CandidateIndexReader
	KNN    *matching.KNN
	Store  *matching.Store
	// Unseen optional override for list/receipts; when nil uses Store.
	Unseen   DigestMatchSource
	EventLog *matching.EventLog
	Reranker matching.Reranker
	Weights  matching.Weights
	// Since bounds the gap-fill look-back window. Defaults to 30 days.
	Since time.Duration
	// DefaultMinScore floors gap-fill when the index has no per-candidate
	// threshold (MATCHING_MIN_SCORE). 0 → 0.70.
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
	// NotificationCli is the platform notification service client.
	NotificationCli notificationv1connect.NotificationServiceClient
	Templates       notify.Templates
	ProfileID       func(ctx context.Context, candidateID string) string
	PublicSiteURL   string
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
	return 0.70
}

type digestRunRequest struct {
	// Cadence: auto | daily | weekly | twice_daily. Empty → DefaultCadence or auto.
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
// subscriber whose notification prefs accept the run cadence it MatchInvokes
// (reason=digest, no row caps / no invoke budget), then emails the top-3
// unseen matches (no receipt yet) and records receipts on successful send.
//
// Request body (optional):
//
//	{"cadence":"auto"|"daily"|"twice_daily"|"weekly"}
//
// auto (default) sends daily users every run, twice_daily users in local
// windows, and weekly users only on WeeklyWeekday in Location.
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
	unseen := deps.Unseen
	if unseen == nil {
		unseen = deps.Store
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

		// Quality floor only — no DailyCap / WeekCount row caps on digest invokes.
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

			var res matching.GapFillResult
			// KNN/Store may be nil in unit tests that only exercise prefs/index skips
			// or inject Unseen fakes for the send path.
			if deps.KNN != nil && deps.Store != nil {
				var runErr error
				res, runErr = matching.MatchInvoke(ctx, matching.InvokeInput{
					CandidateID:    m.ID,
					Embedding:      idx.Embedding,
					Countries:      idx.Countries,
					Kinds:          idx.Kinds,
					SalaryFloorUSD: idx.SalaryFloorUSD,
					Since:          cutoff,
					MinScore:       digestMinScore(idx.MinScore, deps.DefaultMinScore),
					Reason:         matching.InvokeDigest,
					InvokeLimit:    0,
				}, matching.InvokeDeps{
					GapFill: gapDeps,
					Now:     nowFn,
				})
				if runErr != nil {
					log.WithError(runErr).WithField("candidate_id", m.ID).
						Warn("matches-digest: match invoke failed")
					resp.Failed++
					continue
				}
			}

			var top []matching.DigestMatch
			if unseen != nil {
				var lErr error
				top, lErr = unseen.ListTopUnseenMatchesForDigest(ctx, m.ID, "email", 3)
				if lErr != nil {
					log.WithError(lErr).WithField("candidate_id", m.ID).
						Warn("matches-digest: list unseen matches failed")
					resp.Failed++
					continue
				}
			}
			if len(top) == 0 {
				resp.Skipped++
				continue
			}

			rows := make([]eventsv1.MatchRow, 0, len(top))
			for _, t := range top {
				rows = append(rows, eventsv1.MatchRow{
					CanonicalID: t.OpportunityID,
					ApplyURL:    t.ApplyURL,
					Score:       t.Score,
					Title:       t.Title,
					Company:     t.Company,
					Slug:        t.Slug,
				})
			}

			// Primary delivery: NotificationService.Send (profile-style).
			sentOK := false
			if deps.NotificationCli != nil {
				profileID := m.ID
				if deps.ProfileID != nil {
					profileID = deps.ProfileID(ctx, m.ID)
				}
				matchVars := make([]any, 0, len(rows))
				for _, r := range rows {
					matchVars = append(matchVars, map[string]any{
						"canonical_id": r.CanonicalID,
						"title":        r.Title,
						"company":      r.Company,
						"apply_url":    r.ApplyURL,
						"slug":         r.Slug,
						"score":        r.Score,
					})
				}
				site := strings.TrimRight(deps.PublicSiteURL, "/")
				sendErr := notify.Send(ctx, deps.NotificationCli, notify.Message{
					Template:  deps.Templates.Digest(),
					ProfileID: profileID,
					Variables: map[string]any{
						"candidate_id":   m.ID,
						"match_batch_id": res.RunID,
						"count":          float64(len(matchVars)),
						"dashboard_url":  site + "/dashboard/#matches",
						"matches":        matchVars,
					},
				})
				if sendErr != nil {
					log.WithError(sendErr).WithField("candidate_id", m.ID).
						Warn("matches-digest: notify send failed")
					resp.Failed++
					continue
				}
				sentOK = true
				if unseen != nil {
					if rErr := unseen.InsertNotificationReceipts(ctx, m.ID, "email", top); rErr != nil {
						log.WithError(rErr).WithField("candidate_id", m.ID).
							Warn("matches-digest: insert receipts failed")
					}
				}
			} else {
				log.WithField("candidate_id", m.ID).
					Warn("matches-digest: notification client nil — digest not queued")
			}

			// Domain event for bus consumers / analytics (even if notify client nil).
			if deps.Svc != nil {
				env := eventsv1.NewEnvelope(
					eventsv1.TopicCandidateMatchesReady,
					eventsv1.MatchesReadyV1{
						CandidateID:  m.ID,
						MatchBatchID: res.RunID,
						Matches:      rows,
					},
				)
				if emitErr := deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); emitErr != nil {
					log.WithError(emitErr).WithField("candidate_id", m.ID).
						Debug("matches-digest: domain event emit failed")
				}
			}
			if deps.Toucher != nil && (sentOK || deps.NotificationCli == nil) {
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
