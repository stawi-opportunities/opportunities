package archive

import "testing"

func TestRawKey(t *testing.T) {
	got := RawKey("a3f7b2c4d9e2a8f1")
	want := "raw/a3f7b2c4d9e2a8f1.html.gz"
	if got != want {
		t.Errorf("RawKey = %q, want %q", got, want)
	}
}

func TestClusterDir(t *testing.T) {
	got := ClusterDir("cr7qs3q8j1hci9fn3sag")
	want := "clusters/cr7qs3q8j1hci9fn3sag/"
	if got != want {
		t.Errorf("ClusterDir = %q, want %q", got, want)
	}
}

func TestCanonicalKey(t *testing.T) {
	got := CanonicalKey("cr7qs3q8j1hci9fn3sag")
	want := "clusters/cr7qs3q8j1hci9fn3sag/canonical.json"
	if got != want {
		t.Errorf("CanonicalKey = %q, want %q", got, want)
	}
}

func TestVariantKey(t *testing.T) {
	got := VariantKey("cr7qs3q8j1hci9fn3sag", "cr7qr2q8j1hci9fn3sbg")
	want := "clusters/cr7qs3q8j1hci9fn3sag/variants/cr7qr2q8j1hci9fn3sbg.json"
	if got != want {
		t.Errorf("VariantKey = %q, want %q", got, want)
	}
}

func TestManifestKey(t *testing.T) {
	got := ManifestKey("cr7qs3q8j1hci9fn3sag")
	want := "clusters/cr7qs3q8j1hci9fn3sag/manifest.json"
	if got != want {
		t.Errorf("ManifestKey = %q, want %q", got, want)
	}
}
