package archive

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("archive: object not found")

// Archive stores candidate documents outside the job-data pipeline.
// Crawled job payloads are retained directly in PostgreSQL.
type Archive interface {
	PutRaw(ctx context.Context, body []byte) (hash string, size int64, err error)
	GetRaw(ctx context.Context, hash string) ([]byte, error)
	HasRaw(ctx context.Context, hash string) (bool, error)
}
