package cvstore

import (
	"context"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
)

// ArchiveBlobStore stores CV bytes via the product R2 archive (content-addressed).
// Used when the platform files service is not configured.
type ArchiveBlobStore struct {
	Archive archive.Archive
}

// Put archives raw bytes and returns raw/<hash> as ContentURI.
func (s *ArchiveBlobStore) Put(ctx context.Context, in BlobPut) (ContentRef, error) {
	if s == nil || s.Archive == nil {
		return ContentRef{}, fmt.Errorf("cvstore: archive is nil")
	}
	hash, size, err := s.Archive.PutRaw(ctx, in.Body)
	if err != nil {
		return ContentRef{}, fmt.Errorf("cvstore: archive PutRaw: %w", err)
	}
	// Prefer hash of original body for change detection (archive may re-encode).
	contentHash := HashBytes(in.Body)
	if contentHash == "" {
		contentHash = hash
	}
	return ContentRef{
		FileID:      "",
		ContentURI:  archive.RawKey(hash),
		ContentHash: contentHash,
		SizeBytes:   size,
		Storage:     "archive",
	}, nil
}
