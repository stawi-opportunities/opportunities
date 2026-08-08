package candidatestore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// GetCVContactIDs returns standalone ProfileService contact_ids stored for
// this candidate (CV-derived). Empty when column missing or unset.
// These IDs are NOT profile-attached identity contacts (checkout/notify).
func GetCVContactIDs(ctx context.Context, db *sql.DB, candidateID string) ([]string, error) {
	if db == nil || strings.TrimSpace(candidateID) == "" {
		return nil, nil
	}
	var ids pq.StringArray
	err := db.QueryRowContext(ctx, `
SELECT COALESCE(cv_contact_ids, ARRAY[]::text[])
  FROM candidate_profiles
 WHERE id = $1 OR profile_id = $1
 LIMIT 1`, candidateID).Scan(&ids)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		// Column may not exist yet on older DBs.
		if strings.Contains(err.Error(), "cv_contact_ids") {
			return nil, nil
		}
		return nil, fmt.Errorf("candidatestore: get cv_contact_ids: %w", err)
	}
	return cleanIDs(ids), nil
}

// PutCVContactIDs replaces the candidate's list of standalone CV contact_ids.
func PutCVContactIDs(ctx context.Context, db *sql.DB, candidateID string, ids []string) error {
	if db == nil || strings.TrimSpace(candidateID) == "" {
		return nil
	}
	ids = cleanIDs(ids)
	res, err := db.ExecContext(ctx, `
UPDATE candidate_profiles
   SET cv_contact_ids = $2, updated_at = NOW()
 WHERE id = $1 OR profile_id = $1`, candidateID, pq.Array(ids))
	if err != nil {
		if strings.Contains(err.Error(), "cv_contact_ids") {
			return fmt.Errorf("candidatestore: cv_contact_ids column missing — run migration 0027: %w", err)
		}
		return fmt.Errorf("candidatestore: put cv_contact_ids: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrProfileNotFound
	}
	return nil
}

func cleanIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
