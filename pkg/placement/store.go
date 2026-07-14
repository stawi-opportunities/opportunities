package placement

import "context"

// Store persists placement profile summaries.
// Implemented by GormStore (GORM, same style as matching package models).
type Store interface {
	Upsert(ctx context.Context, doc Document) (version int, err error)
	Get(ctx context.Context, candidateID string) (*Document, error)
}
