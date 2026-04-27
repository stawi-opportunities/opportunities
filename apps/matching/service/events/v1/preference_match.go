package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// PreferenceMatchDeps bundles collaborators for the preference-update
// match handler.
type PreferenceMatchDeps struct {
	Svc      *frame.Service
	Match    *httpv1.MatchService
	Matchers *matchers.Registry
	// TopK caps the matches emitted per candidate-kind pair (default 50).
	TopK int
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
		// legacy CV-vs-job KNN pipeline — once per-kind matching is
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

		rows := make([]eventsv1.MatchRow, 0, len(res.Matches))
		for _, m := range res.Matches {
			rows = append(rows, eventsv1.MatchRow{CanonicalID: m.CanonicalID, Score: m.Score})
		}
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
				Warn("preference-match: emit MatchesReadyV1 failed")
			continue
		}
		telemetry.RecordPreferenceTriggeredRun(kind)
		log.WithField("kind", kind).
			WithField("matches", len(rows)).
			Info("preference-match: emitted MatchesReadyV1")
	}
	return nil
}
