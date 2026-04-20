package bloom

import (
	"context"
	"testing"
)

func TestBloomOffsets_Deterministic(t *testing.T) {
	key := "company:title:location:abc123"
	first := bloomOffsets(key)
	second := bloomOffsets(key)
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("bloomOffsets not deterministic at index %d: got %d then %d", i, first[i], second[i])
		}
	}
}

func TestBloomOffsets_InRange(t *testing.T) {
	keys := []string{
		"",
		"simple",
		"company:Software Engineer:New York:12345",
		"acme corp:backend developer:remote:xyz",
	}
	for _, k := range keys {
		offsets := bloomOffsets(k)
		if len(offsets) != numHashes {
			t.Errorf("bloomOffsets(%q) returned %d offsets, want %d", k, len(offsets), numHashes)
		}
		for i, offset := range offsets {
			if offset < 0 || offset >= int64(bitmapSize) {
				t.Errorf("bloomOffsets(%q)[%d] = %d, want [0, %d)", k, i, offset, bitmapSize)
			}
		}
	}
}

func TestMarkSeen_NilValkey_NoPanic(t *testing.T) {
	f := &Filter{valkey: nil, db: nil}
	MarkSeen(context.Background(), f, "src_test_42", "some-hard-key")
}

func TestNewFilter_EmptyAddr_DBOnly(t *testing.T) {
	f := NewFilter("", nil)
	if f == nil {
		t.Fatal("NewFilter returned nil")
	}
	if f.valkey != nil {
		t.Error("expected valkey to be nil for empty addr")
	}
}
