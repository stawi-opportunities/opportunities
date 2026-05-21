package applications_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestMemoryBlobStore_PutGetDelete(t *testing.T) {
	bs := applications.NewMemoryBlobStore()
	ctx := context.Background()

	n, err := bs.Put(ctx, "applications/c1/a1/cv.pdf", "application/pdf", strings.NewReader("hello"))
	require.NoError(t, err)
	require.Equal(t, int64(5), n)

	r, err := bs.Get(ctx, "applications/c1/a1/cv.pdf")
	require.NoError(t, err)
	body, _ := io.ReadAll(r)
	_ = r.Close()
	require.Equal(t, "hello", string(body))

	url, err := bs.PresignGet(ctx, "applications/c1/a1/cv.pdf", time.Minute)
	require.NoError(t, err)
	require.Contains(t, url, "memory://applications/c1/a1/cv.pdf")

	require.NoError(t, bs.Delete(ctx, "applications/c1/a1/cv.pdf"))
	_, err = bs.Get(ctx, "applications/c1/a1/cv.pdf")
	require.ErrorIs(t, err, applications.ErrBlobNotFound)
}

func TestMemoryBlobStore_GetMissing(t *testing.T) {
	bs := applications.NewMemoryBlobStore()
	_, err := bs.Get(context.Background(), "does/not/exist")
	require.ErrorIs(t, err, applications.ErrBlobNotFound)
}

func TestMemoryBlobStore_DeleteNonExistent_NoError(t *testing.T) {
	bs := applications.NewMemoryBlobStore()
	require.NoError(t, bs.Delete(context.Background(), "ghost"))
}

func TestMemoryBlobStore_PresignDefaultTTL(t *testing.T) {
	bs := applications.NewMemoryBlobStore()
	url, err := bs.PresignGet(context.Background(), "some/key", 0)
	require.NoError(t, err)
	require.Contains(t, url, "memory://some/key")
	require.Contains(t, url, "ttl=")
}
