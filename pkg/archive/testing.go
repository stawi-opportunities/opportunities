package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

type FakeArchive struct {
	mu  sync.Mutex
	raw map[string][]byte
}

var _ Archive = (*FakeArchive)(nil)

func NewFakeArchive() *FakeArchive {
	return &FakeArchive{raw: map[string][]byte{}}
}

func (f *FakeArchive) PutRaw(_ context.Context, body []byte) (string, int64, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	f.mu.Lock()
	f.raw[hash] = append([]byte(nil), body...)
	f.mu.Unlock()
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
