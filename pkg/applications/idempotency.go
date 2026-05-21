package applications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// StoredResponse is re-exported for callers that don't pull in
// pkg/httpmw directly. It IS the same type — alias, not value copy —
// so *IdempotencyStore satisfies pkg/httpmw.IdempotencyStore.
type StoredResponse = httpmw.StoredResponse

// IdempotencyStore is the persistent store for replay protection.
//
// Lifecycle: client sends `Idempotency-Key: <key>` → server calls
// Lookup. If a row exists and hasn't expired, the response is replayed.
// Otherwise the server runs the work, then calls Save to record the
// response under the same key. Subsequent calls within the TTL get the
// same response bytes.
type IdempotencyStore struct {
	db  *sql.DB
	ttl time.Duration
}

// NewIdempotencyStore returns a store with the given TTL. Pass 0 for
// the default of 24 hours.
func NewIdempotencyStore(db *sql.DB, ttl time.Duration) *IdempotencyStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &IdempotencyStore{db: db, ttl: ttl}
}

// Lookup returns the stored response for (candidateID, key, routeGroup)
// or `(nil, nil)` if there's no live row (no entry or expired).
func (s *IdempotencyStore) Lookup(ctx context.Context, candidateID, key, routeGroup string) (*StoredResponse, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT status_code, response_body, response_headers
		  FROM idempotency_keys
		 WHERE candidate_id = $1 AND key = $2 AND route_group = $3
		   AND expires_at > now()
	`, candidateID, key, routeGroup)
	var (
		statusCode int
		bodyRaw    []byte
		headersRaw []byte
	)
	err := row.Scan(&statusCode, &bodyRaw, &headersRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("applications: idempotency lookup: %w", err)
	}
	out := &StoredResponse{StatusCode: statusCode, Body: bodyRaw, Headers: map[string]string{}}
	if len(headersRaw) > 0 {
		_ = json.Unmarshal(headersRaw, &out.Headers)
	}
	return out, nil
}

// Save records the response. If a row already exists for the key, the
// existing value is preserved (idempotent — first writer wins).
func (s *IdempotencyStore) Save(ctx context.Context, candidateID, key, routeGroup string, resp StoredResponse) error {
	if resp.Headers == nil {
		resp.Headers = map[string]string{}
	}
	bodyBytes := []byte(resp.Body)
	if len(bodyBytes) == 0 {
		bodyBytes = []byte("null")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO idempotency_keys
		    (candidate_id, key, route_group, status_code, response_body, response_headers, expires_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, now() + $7::interval)
		ON CONFLICT (candidate_id, key, route_group) DO NOTHING
	`,
		candidateID, key, routeGroup,
		resp.StatusCode, bodyBytes, mustEncodeJSON(resp.Headers),
		fmt.Sprintf("%d milliseconds", s.ttl.Milliseconds()),
	)
	if err != nil {
		return fmt.Errorf("applications: idempotency save: %w", err)
	}
	return nil
}

// Purge removes expired rows. Run from a periodic cron; not in the
// request hot path.
func (s *IdempotencyStore) Purge(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM idempotency_keys WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("applications: idempotency purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
