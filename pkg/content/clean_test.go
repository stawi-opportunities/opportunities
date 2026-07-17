package content

import "testing"

func TestToCleanText(t *testing.T) {
	// ToCleanText is plain-text extraction (tags stripped). Storage/display
	// use publish.DescriptionHTML instead.
	cases := []struct {
		name, in, wantContains, wantNotContains string
	}{
		{"raw html", "<p>Für den Ausbau</p>", "Für den Ausbau", "<p>"},
		{"entity-escaped html", "&lt;div class=&quot;intro&quot;&gt;&lt;p&gt;Hello&lt;/p&gt;&lt;/div&gt;", "Hello", "&lt;"},
		{"entity-escaped no double tag", "&lt;div&gt;Market Manager role&lt;/div&gt;", "Market Manager role", "<div"},
		{"already plain", "About DualEntry, a startup.", "About DualEntry", "<"},
		{"empty", "", "", "<"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ToCleanText(c.in)
			if c.wantContains != "" && !contains(got, c.wantContains) {
				t.Errorf("ToCleanText(%q) = %q; want to contain %q", c.in, got, c.wantContains)
			}
			if c.wantNotContains != "" && contains(got, c.wantNotContains) {
				t.Errorf("ToCleanText(%q) = %q; must NOT contain %q", c.in, got, c.wantNotContains)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
