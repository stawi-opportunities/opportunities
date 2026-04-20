// pkg/archive/testing.go
package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
)

// FakeArchive is an in-memory Archive implementation used by
// downstream package tests (crawler, normalize, canonical). It's
// deliberately dumb — no gzip, no network, no retries — so tests
// assert on the logical contract rather than R2 quirks.
type FakeArchive struct {
	mu    sync.Mutex
	raw   map[string][]byte
	blobs map[string][]byte
}

// Compile-time check that FakeArchive satisfies the Archive interface.
// Catches signature drift the moment either side changes.
var _ Archive = (*FakeArchive)(nil)

func NewFakeArchive() *FakeArchive {
	return &FakeArchive{
		raw:   map[string][]byte{},
		blobs: map[string][]byte{},
	}
}

func (f *FakeArchive) PutRaw(_ context.Context, body []byte) (string, int64, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw[hash] = append([]byte(nil), body...)
	return hash, int64(len(body)), nil
}

func (f *FakeArchive) GetRaw(_ context.Context, hash string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.raw[hash]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), body...), nil
}

func (f *FakeArchive) HasRaw(_ context.Context, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.raw[hash]
	return ok, nil
}

func (f *FakeArchive) PutCanonical(_ context.Context, clusterID string, snap CanonicalSnapshot) error {
	return f.putJSON(CanonicalKey(clusterID), snap)
}

func (f *FakeArchive) GetCanonical(_ context.Context, clusterID string) (CanonicalSnapshot, error) {
	var snap CanonicalSnapshot
	err := f.getJSON(CanonicalKey(clusterID), &snap)
	return snap, err
}

func (f *FakeArchive) PutVariant(_ context.Context, clusterID, variantID string, v VariantBlob) error {
	return f.putJSON(VariantKey(clusterID, variantID), v)
}

func (f *FakeArchive) GetVariant(_ context.Context, clusterID, variantID string) (VariantBlob, error) {
	var v VariantBlob
	err := f.getJSON(VariantKey(clusterID, variantID), &v)
	return v, err
}

func (f *FakeArchive) PutManifest(_ context.Context, clusterID string, m Manifest) error {
	return f.putJSON(ManifestKey(clusterID), m)
}

func (f *FakeArchive) GetManifest(_ context.Context, clusterID string) (Manifest, error) {
	var m Manifest
	err := f.getJSON(ManifestKey(clusterID), &m)
	return m, err
}

func (f *FakeArchive) DeleteCluster(_ context.Context, clusterID string) error {
	prefix := ClusterDir(clusterID)
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.blobs {
		if strings.HasPrefix(k, prefix) {
			delete(f.blobs, k)
		}
	}
	return nil
}

func (f *FakeArchive) DeleteRaw(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.raw, hash)
	return nil
}

// Keys returns every blob key currently stored under clusters/.
// Used by tests that want to assert on layout without round-tripping
// via Get*.
func (f *FakeArchive) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.blobs))
	for k := range f.blobs {
		out = append(out, k)
	}
	return out
}

func (f *FakeArchive) putJSON(key string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[key] = body
	return nil
}

func (f *FakeArchive) getJSON(key string, dst any) error {
	f.mu.Lock()
	body, ok := f.blobs[key]
	f.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(body, dst)
}
