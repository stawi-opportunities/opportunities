package matching

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// EventKind enumerates valid candidate_match_events.kind values.
type EventKind string

const (
	EventKindGenerated          EventKind = "generated"
	EventKindViewed             EventKind = "viewed"
	EventKindDismissed          EventKind = "dismissed"
	EventKindOverflow           EventKind = "overflow"
	EventKindRulesChangeRematch EventKind = "rules_change_rematch"
)

// Path enumerates which write path produced an event.
type Path string

const (
	PathFanout          Path = "fanout"
	PathGap             Path = "gap"
	PathCandidateChange Path = "candidate_change"
)

// MatchEvent is one row of candidate_match_events.
type MatchEvent struct {
	EventID       string
	OccurredAt    time.Time
	CandidateID   string
	OpportunityID string
	CanonicalID   string
	Kind          EventKind
	Path          Path
	Score         float64
	RerankScore   *float64
	RerankerUsed  bool
	Data          map[string]any
}

// MatchRunEvent is one row of match_run_events.
type MatchRunEvent struct {
	RunID             string
	StartedAt         time.Time
	FinishedAt        *time.Time
	Path              Path
	TriggeredBy       string
	CandidateID       string
	CanonicalID       string
	CandidatesScanned int
	MatchesWritten    int
	Status            string
	RerankerStatus    string
	LatencyMS         int
	Data              map[string]any
}

// EventLog writes to the two hypertables. Append-only — no UPDATE path.
type EventLog struct {
	db *sql.DB
}

func NewEventLog(db *sql.DB) *EventLog { return &EventLog{db: db} }

const insertMatchEventSQL = `
INSERT INTO candidate_match_events
    (event_id, occurred_at, candidate_id, opportunity_id, canonical_id,
     kind, path, score, rerank_score, reranker_used, data)
VALUES ($1, COALESCE($2, now()), $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
ON CONFLICT (event_id, occurred_at) DO NOTHING
`

func (e *EventLog) WriteMatchEvent(ctx context.Context, m MatchEvent) error {
	data := m.Data
	if data == nil {
		data = map[string]any{}
	}
	occ := nullableTime(m.OccurredAt)
	_, err := e.db.ExecContext(ctx, insertMatchEventSQL,
		m.EventID, occ, m.CandidateID, m.OpportunityID, m.CanonicalID,
		string(m.Kind), string(m.Path), m.Score, nullableF64(m.RerankScore),
		m.RerankerUsed, mustEncodeJSON(data),
	)
	if err != nil {
		return fmt.Errorf("matching: write match event: %w", err)
	}
	return nil
}

const insertRunEventSQL = `
INSERT INTO match_run_events
    (run_id, started_at, finished_at, path, triggered_by,
     candidate_id, canonical_id, candidates_scanned, matches_written,
     status, reranker_status, latency_ms, data)
VALUES ($1, COALESCE($2, now()), $3, $4, $5,
        NULLIF($6, ''), NULLIF($7, ''), $8, $9,
        $10, NULLIF($11, ''), NULLIF($12, 0), $13::jsonb)
ON CONFLICT (run_id, started_at) DO NOTHING
`

func (e *EventLog) WriteMatchRunEvent(ctx context.Context, r MatchRunEvent) error {
	data := r.Data
	if data == nil {
		data = map[string]any{}
	}
	var fin any
	if r.FinishedAt != nil {
		fin = *r.FinishedAt
	}
	_, err := e.db.ExecContext(ctx, insertRunEventSQL,
		r.RunID, nullableTime(r.StartedAt), fin, string(r.Path), r.TriggeredBy,
		r.CandidateID, r.CanonicalID, r.CandidatesScanned, r.MatchesWritten,
		r.Status, r.RerankerStatus, r.LatencyMS, mustEncodeJSON(data),
	)
	if err != nil {
		return fmt.Errorf("matching: write run event: %w", err)
	}
	return nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
