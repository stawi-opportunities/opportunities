package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/billing"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// PreferenceMatchDeps bundles collaborators for the preference-update
// match handler.
type PreferenceMatchDeps struct {
	Svc      *frame.Service
	Match    *httpv1.MatchService
	Matchers *matchers.Registry
	// Persist writes results into candidate_matches so the dashboard feed
	// sees preference-triggered matches (not only MatchesReady events).
	Persist httpv1.MatchPersister
	// WeekCount + Entitlements enforce free/paid weekly remaining on persist.
	WeekCount    httpv1.WeekCounter
	Entitlements func(ctx context.Context, candidateID string) billing.Entitlements
	// TopK caps the matches emitted per candidate-kind pair (default 50).
	TopK int
	// WantsAlerts returns true when the candidate opted into every-match
	// notifications (match_alerts). Digests cover the default summary path.
	WantsAlerts func(ctx context.Context, candidateID string) bool
	// NotificationCli is the platform notification service client.
	NotificationCli notificationv1connect.NotificationServiceClient
	// Templates are MESSAGE_TEMPLATE_* names from config.
	Templates notify.Templates
	// ProfileID resolves candidate_id → platform profile_id for ContactLink.
	// When nil, candidate_id is used as ProfileId.
	ProfileID func(ctx context.Context, candidateID string) string
	// PublicSiteURL for dashboard links in template variables.
	PublicSiteURL string
}

// PreferenceMatchHandler subscribes to TopicCandidatePreferencesUpdated
// and re-runs matching for every kind the candidate has opted into. For
// each enabled kind it consults the matcher registry; disabled matchers
// are skipped. Successful runs emit MatchesReadyV1.
//
// This complements the weekly-digest cron (apps/matching/admin/v1) by
// giving immediate feedback on a preference change rather than making
// the candidate wait until next Monday.
type PreferenceMatchHandler struct {
	deps PreferenceMatchDeps
}

// NewPreferenceMatchHandler wires the handler.
func NewPreferenceMatchHandler(deps PreferenceMatchDeps) *PreferenceMatchHandler {
	if deps.TopK <= 0 {
		deps.TopK = 50
	}
	return &PreferenceMatchHandler{deps: deps}
}

// Name is the topic this handler subscribes to.
func (h *PreferenceMatchHandler) Name() string { return eventsv1.TopicCandidatePreferencesUpdated }

// PayloadType returns the handler's payload type for Frame's
// JSON-decoded event manager.
func (h *PreferenceMatchHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures the payload is non-empty before Execute runs.
func (h *PreferenceMatchHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("preference-match: empty payload")
	}
	return nil
}

// Execute decodes the PreferencesUpdatedV1 envelope, iterates every kind
// in OptIns, and (for enabled matchers) runs the match pipeline + emits
// MatchesReadyV1 capped at TopK results.
func (h *PreferenceMatchHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("preference-match: decode: %w", err)
	}
	in := env.Payload
	if in.CandidateID == "" {
		return errors.New("preference-match: candidate_id required")
	}
	log := util.Log(ctx).WithField("candidate_id", in.CandidateID)

	if h.deps.Matchers == nil || h.deps.Match == nil {
		log.Warn("preference-match: matcher registry or match service not wired; skipping")
		return nil
	}

	for kind := range in.OptIns {
		mt, ok := h.deps.Matchers.For(kind)
		if !ok {
			log.WithField("kind", kind).Debug("preference-match: unknown kind; skipping")
			continue
		}
		if mt.Disabled() {
			log.WithField("kind", kind).Debug("preference-match: matcher disabled; skipping")
			continue
		}

		// Run the underlying match service. Currently this is the
		// CV-vs-job KNN pipeline — once per-kind matching is
		// fully wired it will route through the matcher registry. The
		// per-kind invocation here is the right place to plug that in
		// without touching the weekly-digest path.
		res, err := h.deps.Match.RunMatch(ctx, in.CandidateID)
		if errors.Is(err, httpv1.ErrNoEmbedding) {
			log.WithField("kind", kind).
				Debug("preference-match: no embedding for candidate; skipping")
			continue
		}
		if err != nil {
			log.WithError(err).WithField("kind", kind).
				Warn("preference-match: RunMatch failed")
			continue
		}

		if h.deps.TopK > 0 && len(res.Matches) > h.deps.TopK {
			res.Matches = res.Matches[:h.deps.TopK]
		}

		// Always collect matches for the dashboard (plan caps enforced).
		var caps *httpv1.PersistCaps
		if h.deps.Entitlements != nil {
			ent := h.deps.Entitlements(ctx, in.CandidateID)
			weekUsed := httpv1.LoadWeekUsed(ctx, h.deps.WeekCount, in.CandidateID)
			c := httpv1.CapsFromEntitlements(ent, weekUsed, 0)
			caps = &c
		}
		if err := httpv1.PersistMatchResult(ctx, h.deps.Persist, res, "preference", caps); err != nil {
			log.WithError(err).WithField("kind", kind).
				Warn("preference-match: persist candidate_matches failed")
		}

		rows := make([]eventsv1.MatchRow, 0, len(res.Matches))
		matchVars := make([]any, 0, len(res.Matches))
		for _, m := range res.Matches {
			rows = append(rows, eventsv1.MatchRow{CanonicalID: m.CanonicalID, ApplyURL: m.ApplyURL, Score: m.Score})
			matchVars = append(matchVars, map[string]any{
				"canonical_id": m.CanonicalID,
				"title":        m.Title,
				"company":      m.Company,
				"apply_url":    m.ApplyURL,
				"slug":         m.Slug,
				"score":        m.Score,
			})
		}

		if h.deps.Svc != nil && len(rows) > 0 {
			readyEnv := eventsv1.NewEnvelope(
				eventsv1.TopicCandidateMatchesReady,
				eventsv1.MatchesReadyV1{
					CandidateID:  res.CandidateID,
					MatchBatchID: res.MatchBatchID,
					Matches:      rows,
				},
			)
			if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, readyEnv); err != nil {
				log.WithError(err).WithField("kind", kind).
					Debug("preference-match: domain event emit failed")
			}
		}

		// User notify only when match_alerts; always via NotificationService.Send.
		wants := h.deps.WantsAlerts != nil && h.deps.WantsAlerts(ctx, in.CandidateID)
		if wants && len(matchVars) > 0 && h.deps.NotificationCli != nil {
			profileID := res.CandidateID
			if h.deps.ProfileID != nil {
				profileID = h.deps.ProfileID(ctx, res.CandidateID)
			}
			site := strings.TrimRight(h.deps.PublicSiteURL, "/")
			_ = notify.Send(ctx, h.deps.NotificationCli, notify.Message{
				Template:  h.deps.Templates.Ready(),
				ProfileID: profileID,
				Variables: map[string]any{
					"candidate_id":   res.CandidateID,
					"match_batch_id": res.MatchBatchID,
					"count":          float64(len(matchVars)),
					"dashboard_url":  site + "/dashboard/#matches",
					"matches":        matchVars,
				},
				Priority:    notificationv1.PRIORITY_HIGH,
				PrioritySet: true,
			})
		}

		telemetry.RecordPreferenceTriggeredRun(kind)
		log.WithField("kind", kind).
			WithField("matches", len(rows)).
			WithField("notify", wants).
			Info("preference-match: matches collected")
	}
	return nil
}
