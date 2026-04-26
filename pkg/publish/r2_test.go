package publish

import "testing"

func TestObjectKey_ByKind(t *testing.T) {
	cases := []struct{ prefix, slug, want string }{
		{"jobs", "go-eng-acme-abc", "jobs/go-eng-acme-abc.json"},
		{"scholarships", "msc-eth-xyz", "scholarships/msc-eth-xyz.json"},
		{"tenders", "rfp-foo", "tenders/rfp-foo.json"},
	}
	for _, c := range cases {
		if got := ObjectKey(c.prefix, c.slug); got != c.want {
			t.Errorf("ObjectKey(%q,%q) = %q, want %q", c.prefix, c.slug, got, c.want)
		}
	}
}

func TestTranslationKey_ByKind(t *testing.T) {
	got := TranslationKey("scholarships", "msc-eth-xyz", "fr")
	want := "scholarships/msc-eth-xyz/fr.json"
	if got != want {
		t.Errorf("TranslationKey = %q, want %q", got, want)
	}
}
