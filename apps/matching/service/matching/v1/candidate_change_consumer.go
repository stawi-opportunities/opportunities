package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	util "github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/billing"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// candidateTextLookup composes the cross-encoder query text for a candidate
// from the candidate_profiles row. Returns "" (not an error) when the row is
// absent or empty so the caller can degrade the reranker to a no-op.
type candidateTextLookup interface {
	QueryText(ctx context.Context, candidateID string) (string, error)
}

// sqlCandidateText reads candidate_profiles to build the rerank query text.
type sqlCandidateText struct{ db *sql.DB }

// NewSQLCandidateText constructs a candidateTextLookup backed by db.
func NewSQLCandidateText(db *sql.DB) candidateTextLookup { return &sqlCandidateText{db: db} }

// candidateTextSQL reads the rerank query fields. strong_skills is a
// text[] (array_to_string); preferred_roles is a TEXT column that stores an
// array-literal string (e.g. '{"Backend Engineer","Platform Engineer"}'), so
// translate() strips the braces/quotes rather than array_to_string (which
// would error on a non-array type).
const candidateTextSQL = `
SELECT COALESCE(current_title,''),
       COALESCE(seniority,''),
       COALESCE(array_to_string(strong_skills, ', '),''),
       COALESCE(translate(preferred_roles, '{}"', ''),''),
       COALESCE(bio,'')
FROM candidate_profiles
WHERE id = $1
`

func (s *sqlCandidateText) QueryText(ctx context.Context, candidateID string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	var title, seniority, skills, roles, bio string
	err := s.db.QueryRowContext(ctx, candidateTextSQL, candidateID).
		Scan(&title, &seniority, &skills, &roles, &bio)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("matching: candidate profile text %s: %w", candidateID, err)
	}
	var parts []string
	if title != "" {
		parts = append(parts, title+".")
	}
	if seniority != "" {
		parts = append(parts, "seniority: "+seniority+".")
	}
	if skills != "" {
		parts = append(parts, "skills: "+skills+".")
	}
	if roles != "" {
		parts = append(parts, "roles: "+roles+".")
	}
	if bio != "" {
		parts = append(parts, bio)
	}
	return strings.TrimSpace(strings.Join(parts, " ")), nil
}

// CandidateChangeConsumerDeps gathers the deps for one consumer.
type CandidateChangeConsumerDeps struct {
	IndexStore *matching.IndexStore
	KNN        *matching.KNN
	Store      *matching.Store
	EventLog   *matching.EventLog
	Reranker   matching.Reranker
	Weights    matching.Weights
	Debouncer  matching.Debouncer
	DLQ        *DLQGuard
	Topic      string // TopicCandidatePreferencesUpdated or TopicCandidateEmbedding
	// CandText composes the cross-encoder query text from candidate_profiles.
	// Optional: nil disables reranking (QueryText stays "").
	CandText candidateTextLookup
	// DefaultMinScore floors automatic match generation (MATCHING_MIN_SCORE).
	// 0 falls back to 0.45 so paid users still receive quality matches without
	// starving the queue on a too-high default.
	DefaultMinScore float64
	// DailyCapQuery enforces plan daily limits during GapFill (optional).
	DailyCapQuery matching.DailyCapQuery
}

// CandidateChangeConsumer subscribes to one trigger topic and runs Path C.
type CandidateChangeConsumer struct {
	deps CandidateChangeConsumerDeps
}

// NewCandidateChangeConsumer wires the consumer.
func NewCandidateChangeConsumer(d CandidateChangeConsumerDeps) *CandidateChangeConsumer {
	return &CandidateChangeConsumer{deps: d}
}

// Name implements queue.SubscribeWorker — the queue subject this consumer
// drains (TopicCandidateEmbedding).
func (c *CandidateChangeConsumer) Name() string { return c.deps.Topic }

// Handle implements queue.SubscribeWorker: drains the dedicated candidate-
// embedding queue. Frame surfaces JetStream metadata (including redelivery
// count) via the headers map.
func (c *CandidateChangeConsumer) Handle(ctx context.Context, headers map[string]string, payload []byte) error {
	redelivery := parseRedeliveryHeader(headers)
	return c.deps.DLQ.Run(ctx, redelivery, payload, func() error {
		return c.handleOnce(ctx, payload)
	})
}

func parseRedeliveryHeader(headers map[string]string) int {
	for _, key := range []string{"Nats-Redelivery-Count", "nats-redelivery-count", "redelivery"} {
		if value, ok := headers[key]; ok {
			var count int
			_, _ = fmt.Sscanf(value, "%d", &count)
			return count
		}
	}
	return 0
}

func (c *CandidateChangeConsumer) handleOnce(ctx context.Context, payload []byte) error {
	candidateID, triggeredBy, vector, err := decodeCandidateChange(c.deps.Topic, payload)
	if err != nil {
		return err
	}

	change := matching.CandidateChange{
		CandidateID: candidateID,
		TriggeredBy: triggeredBy,
	}

	// Auto-populate / refresh candidate_match_indexes from the embedding event
	// (folds in what the standalone indexer did) so the index always has a
	// current vector for fan-out + future preference-change passes. Preserve
	// any existing prefs; default a brand-new row using plan entitlements.
	defaultMin := c.deps.DefaultMinScore
	if defaultMin <= 0 || defaultMin > 1 {
		defaultMin = 0.45
	}

	if len(vector) > 0 {
		// Dual-writer protection: persona (conversation+CV) owns the index vector.
		// Thin async CV-field embeds must not clobber rerank_text-backed personas.
		var embEnv eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
		_ = json.Unmarshal(payload, &embEnv)
		src := strings.TrimSpace(embEnv.Payload.Source)
		existing, gErr := c.deps.IndexStore.Get(ctx, candidateID)
		personaOwned := gErr == nil && existing != nil && strings.TrimSpace(existing.RerankText) != ""
		skipVectorUpsert := personaOwned && src != eventsv1.EmbeddingSourcePersona
		if skipVectorUpsert {
			util.Log(ctx).WithField("candidate_id", candidateID).WithField("source", src).
				Info("candidate_change: skip thin embed overwrite of persona index")
		} else {
			ent := planEntitlements(ctx, c.deps.IndexStore, candidateID)
			ci := matching.CandidateIndex{
				CandidateID: candidateID,
				Embedding:   vector,
				MinScore:    defaultMin,
				DailyCap:    ent.DailyCap,
				WeeklyCap:   ent.WeeklyCap,
				Kinds:       []string{"job"},
				Enabled:     true,
			}
			if gErr == nil && existing != nil {
				if existing.MinScore > 0 {
					ci.MinScore = existing.MinScore
				}
				if existing.DailyCap > 0 {
					ci.DailyCap = existing.DailyCap
				}
				ci.WeeklyCap = existing.WeeklyCap
				ci.Kinds = existing.Kinds
				ci.Countries = existing.Countries
				ci.SalaryFloorUSD = existing.SalaryFloorUSD
				ci.RemoteOnly = existing.RemoteOnly
				ci.RerankText = existing.RerankText
			}
			if uErr := c.deps.IndexStore.Upsert(ctx, ci); uErr != nil {
				util.Log(ctx).WithError(uErr).WithField("candidate_id", candidateID).
					Warn("candidate_change: index upsert failed (non-fatal)")
			}
		}
	}

	idx, err := c.deps.IndexStore.Get(ctx, candidateID)
	switch {
	case err == nil:
		change.Embedding = idx.Embedding
		change.Countries = idx.Countries
		change.Kinds = idx.Kinds
		change.SalaryFloorUSD = idx.SalaryFloorUSD
		change.MinScore = idx.MinScore
		change.DailyCap = idx.DailyCap
		change.WeeklyCap = idx.WeeklyCap
		if change.MinScore <= 0 {
			change.MinScore = defaultMin
		}
	case errors.Is(err, matching.ErrNotFound) && len(vector) > 0:
		// Race: the index row hasn't been written yet but the embedding event
		// carries the vector. Run with sensible defaults so the candidate still
		// gets a gap-fill pass.
		util.Log(ctx).WithField("candidate_id", candidateID).
			Debug("candidate_change: no index row yet; using event vector + default prefs")
		change.Embedding = vector
		change.MinScore = defaultMin
		change.Kinds = []string{"job"}
		ent := planEntitlements(ctx, c.deps.IndexStore, candidateID)
		change.DailyCap = ent.DailyCap
		change.WeeklyCap = ent.WeeklyCap
	case errors.Is(err, matching.ErrNotFound):
		util.Log(ctx).WithField("candidate_id", candidateID).
			Info("candidate_change: no index row yet; skip")
		return nil
	default:
		return fmt.Errorf("matching: candidate change load index %s: %w", candidateID, err)
	}

	// Cross-encoder query text from candidate_profiles. Soft-fail: on lookup
	// error or empty result, QueryText stays "" and the reranker no-ops.
	if c.deps.CandText != nil {
		qt, qErr := c.deps.CandText.QueryText(ctx, candidateID)
		if qErr != nil {
			util.Log(ctx).WithError(qErr).WithField("candidate_id", candidateID).
				Debug("candidate_change: query-text lookup failed; reranker disabled for this run")
		} else {
			change.QueryText = qt
		}
	}

	_, err = matching.RunCandidateChange(ctx, change, matching.CandidateChangeDeps{
		Debouncer: c.deps.Debouncer,
		GapFill: matching.GapFillDeps{
			KNN:       c.deps.KNN,
			Store:     c.deps.Store,
			EventLog:  c.deps.EventLog,
			Reranker:  c.deps.Reranker,
			Weights:   c.deps.Weights,
			DailyCap:  c.deps.DailyCapQuery,
			WeekCount: c.deps.Store,
		},
	})
	if errors.Is(err, matching.ErrDebounced) {
		util.Log(ctx).WithField("candidate_id", candidateID).
			Info("candidate_change: debounced")
		return nil
	}
	return err
}

// planEntitlements maps subscription + plan_id to caps. Free/cancelled/empty
// subscription always gets free-proof caps even if onboard stored a sellable plan_id.
func planEntitlements(ctx context.Context, idx *matching.IndexStore, candidateID string) billing.Entitlements {
	if idx == nil {
		return billing.EntitlementsFor("")
	}
	planID, sub, err := idx.CandidatePlanAndSubscription(ctx, candidateID)
	if err != nil {
		return billing.EntitlementsFor("")
	}
	switch strings.ToLower(strings.TrimSpace(sub)) {
	case "paid", "past_due", "trial":
		if planID == "" {
			return billing.EntitlementsFor(billing.PlanStarter)
		}
		return billing.EntitlementsFor(billing.PlanID(planID))
	default:
		// free, cancelled, empty — proof caps only
		return billing.EntitlementsFor("")
	}
}

// decodeCandidateChange extracts the candidate_id, a TriggeredBy label, and
// (for embedding events) the carried vector from the topic-specific payload.
// Both event types (PreferencesUpdatedV1, CandidateEmbeddingV1) use the
// JSON field name "candidate_id" — confirmed from pkg/events/v1/candidates.go.
func decodeCandidateChange(topic string, payload []byte) (candidateID, triggeredBy string, vector []float32, err error) {
	switch topic {
	case eventsv1.TopicCandidatePreferencesUpdated:
		var env eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
		if err = json.Unmarshal(payload, &env); err != nil {
			return "", "", nil, fmt.Errorf("matching: candidate change decode prefs: %w", err)
		}
		return env.Payload.CandidateID, "rules_changed", nil, nil
	case eventsv1.TopicCandidateEmbedding:
		var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
		if err = json.Unmarshal(payload, &env); err != nil {
			return "", "", nil, fmt.Errorf("matching: candidate change decode embed: %w", err)
		}
		return env.Payload.CandidateID, "cv_changed", env.Payload.Vector, nil
	}
	return "", "", nil, fmt.Errorf("matching: candidate change unknown topic %q", topic)
}
