package service

import (
	"net/url"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stretchr/testify/assert"
)

func TestClassifyATS(t *testing.T) {
	cases := []struct {
		in     string
		typ    domain.SourceType
		base   string
		wantOK bool
	}{
		{"https://jobs.lever.co/netflix/abc-123", domain.SourceLever, "https://jobs.lever.co/netflix", true},
		{"https://www.jobs.lever.co/acme", domain.SourceLever, "https://jobs.lever.co/acme", true},
		{"https://boards.greenhouse.io/stripe/jobs/123", domain.SourceGreenhouse, "https://boards.greenhouse.io/stripe", true},
		{"https://job-boards.greenhouse.io/figma", domain.SourceGreenhouse, "https://job-boards.greenhouse.io/figma", true},
		{"https://careers.smartrecruiters.com/Visa/123", domain.SourceSmartRecruitersPage, "https://careers.smartrecruiters.com/Visa", true},
		{"https://jobs.lever.co/", "", "", false},       // ATS host, no company
		{"https://ethiojobs.net/jobs/x", "", "", false}, // not an ATS
	}
	for _, c := range cases {
		u, err := url.Parse(c.in)
		assert.NoError(t, err, c.in)
		typ, base, ok := classifyATS(u)
		assert.Equal(t, c.wantOK, ok, c.in)
		if c.wantOK {
			assert.Equal(t, c.typ, typ, c.in)
			assert.Equal(t, c.base, base, c.in)
		}
	}
}
