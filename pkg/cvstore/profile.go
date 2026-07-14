package cvstore

import (
	"context"
	"database/sql"
	"fmt"
)

// SQLProfilePointers updates candidate_profiles CV columns.
type SQLProfilePointers struct {
	DB *sql.DB
}

// SetCVPointers writes durable CV storage fields. Best-effort: if the
// profile row does not exist yet (pre-onboard chat), the update is a no-op.
func (p *SQLProfilePointers) SetCVPointers(ctx context.Context, candidateID, contentURI, contentHash, cvURL string) error {
	if p == nil || p.DB == nil {
		return fmt.Errorf("cvstore: db is nil")
	}
	if candidateID == "" {
		return fmt.Errorf("cvstore: candidate_id required")
	}
	// Columns exist on candidate_profiles (domain.CandidateProfile).
	// Use COALESCE so empty args don't wipe a prior value when only one changes.
	res, err := p.DB.ExecContext(ctx, `
UPDATE candidate_profiles SET
    cv_storage_uri  = CASE WHEN $2 <> '' THEN $2 ELSE cv_storage_uri END,
    cv_content_hash = CASE WHEN $3 <> '' THEN $3 ELSE cv_content_hash END,
    cv_url          = CASE WHEN $4 <> '' THEN $4 ELSE cv_url END,
    updated_at      = now()
WHERE id = $1 OR profile_id = $1
`, candidateID, contentURI, contentHash, cvURL)
	if err != nil {
		return fmt.Errorf("cvstore: set cv pointers: %w", err)
	}
	// No rows is OK — seeker may upload during chat before onboard creates the row.
	_, _ = res.RowsAffected()
	return nil
}
