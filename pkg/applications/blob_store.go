package applications

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// BlobStore is the abstract blob backend. The R2 implementation is used in
// production; tests use the in-memory MemoryBlobStore.
//
// Key conventions (for reference; not enforced here):
//
//	applications/<candidate_id>/<application_id>/<filename>
type BlobStore interface {
	// Put uploads body under key with the given content-type and returns the
	// number of bytes written.
	Put(ctx context.Context, key, contentType string, body io.Reader) (int64, error)

	// Get returns the blob body. The caller must close the returned reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the blob. Deleting a non-existent key is a no-op.
	Delete(ctx context.Context, key string) error

	// PresignGet returns a time-limited URL giving unauthenticated read access
	// to the blob. The R2 implementation signs the request with HMAC-SHA256;
	// the memory implementation returns a pseudo-URL for testing only.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ErrBlobNotFound is returned by BlobStore.Get when no blob exists at key.
var ErrBlobNotFound = errors.New("applications: blob not found")

// ---- MemoryBlobStore --------------------------------------------------------

// MemoryBlobStore is process-local; suitable for tests and dev only.
// It is safe for concurrent use.
type MemoryBlobStore struct {
	mu   sync.Mutex
	data map[string]memoryBlob
}

type memoryBlob struct {
	body        []byte
	contentType string
}

// NewMemoryBlobStore returns an empty in-memory store.
func NewMemoryBlobStore() *MemoryBlobStore {
	return &MemoryBlobStore{data: map[string]memoryBlob{}}
}

// Put stores body in memory under key.
func (m *MemoryBlobStore) Put(_ context.Context, key, contentType string, body io.Reader) (int64, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = memoryBlob{body: b, contentType: contentType}
	return int64(len(b)), nil
}

// Get retrieves the blob stored under key.
func (m *MemoryBlobStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, ErrBlobNotFound
	}
	return io.NopCloser(bytes.NewReader(b.body)), nil
}

// Delete removes the blob at key. Deleting a non-existent key is a no-op.
func (m *MemoryBlobStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// PresignGet returns a pseudo-URL of the form memory://<key>?ttl=<duration>.
// The production R2 implementation substitutes a real signed URL.
func (m *MemoryBlobStore) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return "memory://" + key + "?ttl=" + ttl.String(), nil
}

// ---- R2BlobStore stub -------------------------------------------------------

// R2BlobStore implements BlobStore against Cloudflare R2.
//
// Construction:
//
//	store := NewR2BlobStore(R2Config{
//	    AccountID:       os.Getenv("R2_ACCOUNT_ID"),
//	    AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
//	    SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
//	    Bucket:          os.Getenv("R2_BUCKET"),
//	    PublicBaseURL:   os.Getenv("R2_PUBLIC_BASE_URL"), // optional, for presigned URLs
//	})
//
// The implementation uses the AWS S3 SDK v2 pointed at the R2 endpoint
// (https://<accountID>.r2.cloudflarestorage.com). Presigned URLs are
// generated with s3.NewPresignClient.
//
// This type is declared here so callers can reference R2BlobStore in
// production wiring without importing a separate package. The implementation
// body is omitted to keep this package free of heavy SDK dependencies at
// compile time; add it in an adjacent r2_blob_store.go when wiring the
// production binary.
type R2BlobStore struct{}
