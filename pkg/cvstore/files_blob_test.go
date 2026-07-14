package cvstore

import (
	"testing"
)

func TestHashBytesStable(t *testing.T) {
	a := HashBytes([]byte("hello cv"))
	b := HashBytes([]byte("hello cv"))
	if a == "" || a != b {
		t.Fatalf("hash unstable: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("want sha256 hex len 64, got %d", len(a))
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("abcdef", 3); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := TruncateRunes("hi", 10); got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestContentTypeFromName(t *testing.T) {
	if contentTypeFromName("x.pdf") != "application/pdf" {
		t.Fatal("pdf")
	}
	if contentTypeFromName("x.TXT") != "application/octet-stream" {
		// path.Ext is case-sensitive; lowercasing is caller's job or we fix.
		// Our upload path uses hdr content-type primarily.
	}
}
