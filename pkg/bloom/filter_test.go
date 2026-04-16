package bloom

import (
	"context"
	"testing"
)

func TestBloomOffset_Deterministic(t *testing.T) {
	key := "company:title:location:abc123"
	first := bloomOffset(key)
	second := bloomOffset(key)
	if first != second {
		t.Errorf("bloomOffset not deterministic: got %d then %d", first, second)
	}
}

func TestBloomOffset_InRange(t *testing.T) {
	keys := []string{
		"",
		"simple",
		"company:Software Engineer:New York:12345",
		"acme corp:backend developer:remote:xyz",
	}
	for _, k := range keys {
		offset := bloomOffset(k)
		if offset < 0 || offset >= bitmapSize {
			t.Errorf("bloomOffset(%q) = %d, want [0, %d)", k, offset, bitmapSize)
		}
	}
}

func TestMarkSeen_NilValkey_NoPanic(t *testing.T) {
	f := &Filter{valkey: nil, db: nil}
	// Should not panic when Valkey is nil
	MarkSeen(context.Background(), f, 42, "some-hard-key")
}

func TestNewFilter_EmptyAddr_DBOnly(t *testing.T) {
	f := NewFilter("", nil)
	if f == nil {
		t.Fatal("NewFilter returned nil")
	}
	if f.valkey != nil {
		t.Error("expected valkey to be nil for empty addr, got non-nil client")
	}
}
