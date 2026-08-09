package matching

import (
	"context"
	"fmt"
	"strings"
)

// ListTopUnseenMatchesForDigest returns the highest-scoring non-overflow,
// non-dismissed matches that have no notification receipt for channel
// (default "email"). Cap limit at 3 when callers pass ≤0 or >3.
func (s *Store) ListTopUnseenMatchesForDigest(ctx context.Context, candidateID, channel string, limit int) ([]DigestMatch, error) {
	if limit <= 0 || limit > 3 {
		limit = 3
	}
	channel = strings.TrimSpace(channel)
	if channel == "" {
		channel = "email"
	}
	const q = `
SELECT m.match_id,
       m.opportunity_id,
       COALESCE(o.apply_url, ''),
       m.score,
       COALESCE(o.title, ''),
       COALESCE(o.issuing_entity, ''),
       COALESCE(o.slug, '')
FROM candidate_matches m
JOIN opportunities o ON o.canonical_id = m.opportunity_id
WHERE m.candidate_id = $1
  AND m.status NOT IN ('overflow', 'dismissed')
  AND NOT EXISTS (
    SELECT 1 FROM match_notification_receipts r
     WHERE r.candidate_id = m.candidate_id
       AND r.match_id = m.match_id
       AND r.channel = $2
  )
ORDER BY m.score DESC, m.created_at DESC
LIMIT $3`
	rows, err := s.db.QueryContext(ctx, q, candidateID, channel, limit)
	if err != nil {
		return nil, fmt.Errorf("matching: list unseen digest matches: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]DigestMatch, 0, limit)
	for rows.Next() {
		var d DigestMatch
		if err := rows.Scan(&d.MatchID, &d.OpportunityID, &d.ApplyURL, &d.Score, &d.Title, &d.Company, &d.Slug); err != nil {
			return nil, fmt.Errorf("matching: scan unseen digest match: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// InsertNotificationReceipts upserts receipt rows for each digest match so
// subsequent digests exclude them. Empty channel defaults to "email".
// Items without MatchID or OpportunityID are skipped.
func (s *Store) InsertNotificationReceipts(ctx context.Context, candidateID, channel string, items []DigestMatch) error {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		channel = "email"
	}
	const q = `
INSERT INTO match_notification_receipts (candidate_id, match_id, opportunity_id, channel, sent_at)
VALUES ($1,$2,$3,$4,now())
ON CONFLICT (candidate_id, match_id, channel) DO NOTHING`
	for _, it := range items {
		if strings.TrimSpace(it.MatchID) == "" || strings.TrimSpace(it.OpportunityID) == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, q, candidateID, it.MatchID, it.OpportunityID, channel); err != nil {
			return fmt.Errorf("matching: insert notification receipt: %w", err)
		}
	}
	return nil
}
