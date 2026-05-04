package browser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeBaseName(t *testing.T) {
	cases := map[string]string{
		"resume.pdf":           "resume.pdf",
		"":                     "resume.pdf",
		"/abs/path/cv.pdf":     "cv.pdf",
		"../../etc/passwd":     "passwd",
		"中文.pdf":               ".pdf",
		strings.Repeat("a", 200) + ".pdf": strings.Repeat("a", 64),
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, safeBaseName(in))
		})
	}
}

func TestHostBucket(t *testing.T) {
	cases := map[string]string{
		"https://boards.greenhouse.io/co/jobs/1":      "boards.greenhouse.io",
		"https://jobs.lever.co/co/1":                  "jobs.lever.co",
		"https://co.wd5.myworkdayjobs.com/external/1": "myworkdayjobs.com",
		"https://jobs.smartrecruiters.com/co/1":       "smartrecruiters.com",
		"https://random.example.com/apply":            "other",
		"":                                            "other",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, hostBucket(in))
		})
	}
}
