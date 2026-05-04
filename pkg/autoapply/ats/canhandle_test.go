package ats

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestSplitName(t *testing.T) {
	cases := map[string][2]string{
		"":                  {"", ""},
		"Ada":               {"Ada", ""},
		"Ada Lovelace":      {"Ada", "Lovelace"},
		"Anne Marie Smith":  {"Anne", "Marie Smith"},
		"  Ada   Lovelace ": {"Ada", "Lovelace"},
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			f, l := splitName(in)
			assert.Equal(t, want[0], f)
			assert.Equal(t, want[1], l)
		})
	}
}

func TestGreenhouse_CanHandle(t *testing.T) {
	s := NewGreenhouseSubmitter(nil)
	assert.True(t, s.CanHandle(domain.SourceGreenhouse, ""))
	assert.True(t, s.CanHandle("", "https://boards.greenhouse.io/co/jobs/1"))
	assert.True(t, s.CanHandle("", "https://co.com/jobs?gh_jid=42"))
	assert.True(t, s.CanHandle("", "https://co.com/greenhouse.io/jobs"))
	assert.False(t, s.CanHandle("", "https://jobs.lever.co/co/1"))
}

func TestLever_CanHandle(t *testing.T) {
	s := NewLeverSubmitter(nil)
	assert.True(t, s.CanHandle(domain.SourceLever, ""))
	assert.True(t, s.CanHandle("", "https://jobs.lever.co/co/1"))
	assert.True(t, s.CanHandle("", "https://lever.co/co/1"))
	assert.False(t, s.CanHandle("", "https://co.com/apply"))
}

func TestWorkday_CanHandle(t *testing.T) {
	s := NewWorkdaySubmitter(nil)
	assert.True(t, s.CanHandle(domain.SourceWorkday, ""))
	assert.True(t, s.CanHandle("", "https://co.wd5.myworkdayjobs.com/external/job/1"))
	assert.True(t, s.CanHandle("", "https://wd3.myworkday.com/co/d/1"))
	assert.False(t, s.CanHandle("", "https://boards.greenhouse.io/co/jobs/1"))
}

func TestSmartRecruiters_CanHandle(t *testing.T) {
	s := NewSmartRecruitersSubmitter(nil)
	assert.True(t, s.CanHandle(domain.SourceSmartRecruitersAPI, ""))
	assert.True(t, s.CanHandle(domain.SourceSmartRecruitersPage, ""))
	assert.True(t, s.CanHandle("", "https://jobs.smartrecruiters.com/co/1"))
	assert.False(t, s.CanHandle("", "https://jobs.lever.co/co/1"))
}
