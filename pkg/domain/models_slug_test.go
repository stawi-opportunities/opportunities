package domain

import "testing"

func TestBuildSlug_PerKind(t *testing.T) {
	cases := []struct {
		kind, title, issuer, hash, want string
	}{
		{"job", "Senior Go Engineer", "Acme", "ab12cd34", "senior-go-engineer-at-acme-ab12cd34"},
		{"scholarship", "MSc Climate Science", "ETH Zurich", "ab12cd34", "msc-climate-science-at-eth-zurich-ab12cd34"},
		{"tender", "School Construction", "Min of Education", "ab12cd34", "school-construction-from-min-of-education-ab12cd34"},
		{"deal", "20% off Notion", "Notion Labs", "ab12cd34", "20-off-notion-at-notion-labs-ab12cd34"},
		{"funding", "Climate Tech Grant", "Bezos Earth Fund", "ab12cd34", "climate-tech-grant-from-bezos-earth-fund-ab12cd34"},
	}
	for _, c := range cases {
		got := BuildSlug(c.kind, c.title, c.issuer, c.hash)
		if got != c.want {
			t.Errorf("BuildSlug(%q,%q,%q,%q) = %q, want %q", c.kind, c.title, c.issuer, c.hash, got, c.want)
		}
	}
}
