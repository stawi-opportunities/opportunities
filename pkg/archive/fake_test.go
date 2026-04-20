package archive

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeArchive_RoundTripRaw(t *testing.T) {
	a := NewFakeArchive()
	ctx := context.Background()

	body := []byte("<html>hi</html>")
	hash, size, err := a.PutRaw(ctx, body)
	if err != nil {
		t.Fatalf("PutRaw: %v", err)
	}
	if size != int64(len(body)) {
		t.Errorf("size = %d, want %d", size, len(body))
	}

	ok, err := a.HasRaw(ctx, hash)
	if err != nil || !ok {
		t.Fatalf("HasRaw = (%v, %v)", ok, err)
	}

	got, err := a.GetRaw(ctx, hash)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("GetRaw = %q, want %q", got, body)
	}
}

func TestFakeArchive_RawMiss(t *testing.T) {
	a := NewFakeArchive()
	_, err := a.GetRaw(context.Background(), "deadbeef")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFakeArchive_CanonicalAndManifest(t *testing.T) {
	a := NewFakeArchive()
	ctx := context.Background()
	now := time.Now().UTC()

	snap := CanonicalSnapshot{ID: "c1", ClusterID: "c1", Slug: "foo", WrittenAt: now}
	if err := a.PutCanonical(ctx, "c1", snap); err != nil {
		t.Fatalf("PutCanonical: %v", err)
	}
	got, err := a.GetCanonical(ctx, "c1")
	if err != nil || got.Slug != "foo" {
		t.Errorf("GetCanonical: %+v err=%v", got, err)
	}

	m := Manifest{ClusterID: "c1", CanonicalID: "c1", Slug: "foo", UpdatedAt: now}
	if err := a.PutManifest(ctx, "c1", m); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	gotM, _ := a.GetManifest(ctx, "c1")
	if gotM.CanonicalID != "c1" {
		t.Errorf("GetManifest = %+v", gotM)
	}
}

func TestFakeArchive_DeleteCluster(t *testing.T) {
	a := NewFakeArchive()
	ctx := context.Background()
	_ = a.PutCanonical(ctx, "c1", CanonicalSnapshot{ID: "c1"})
	_ = a.PutVariant(ctx, "c1", "v1", VariantBlob{ID: "v1"})

	if err := a.DeleteCluster(ctx, "c1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	if _, err := a.GetCanonical(ctx, "c1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("canonical should be gone, got %v", err)
	}
	if _, err := a.GetVariant(ctx, "c1", "v1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("variant should be gone, got %v", err)
	}
}
