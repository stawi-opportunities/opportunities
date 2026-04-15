package indexer

import (
	"context"

	"stawi.jobs/internal/domain"
)

type Indexer interface {
	IndexCanonicalJob(ctx context.Context, job domain.CanonicalJob) error
	Search(ctx context.Context, query string, limit int) ([]domain.CanonicalJob, error)
}
