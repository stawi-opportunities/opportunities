package archive

import (
	"context"
	"errors"
	"testing"
)

func TestFakeArchiveRoundTrip(t *testing.T) {
	store := NewFakeArchive()
	body := []byte("candidate document")
	hash, size, err := store.PutRaw(context.Background(), body)
	if err != nil || size != int64(len(body)) {
		t.Fatalf("PutRaw() = (%q, %d, %v)", hash, size, err)
	}
	got, err := store.GetRaw(context.Background(), hash)
	if err != nil || string(got) != string(body) {
		t.Fatalf("GetRaw() = (%q, %v)", got, err)
	}
	if _, err := store.GetRaw(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing GetRaw() error = %v", err)
	}
}

func TestRawKey(t *testing.T) {
	if got := RawKey("abc"); got != "raw/abc.html.gz" {
		t.Fatalf("RawKey() = %q", got)
	}
}
