package v1

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComposeReply_PrefersLLMWhenNotReady(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{
		TargetJobTitle:     "Engineer",
		PreferredCountries: []string{"ZA"},
		ExtraInfo:          "Uploaded CV on file. Resume document stored for matching (experience, education, skills).",
	}
	missing := []string{"target_job_title", "job_types", "salary_expectation", "experience_level"}
	// Even if missing still has title (edge), LLM meta answer must win.
	llm := "Yes — this chat is onboarding. I already see a CV and ZA markets; next I need a concrete role title so we can score jobs fairly."
	got := composeReply(llm, f, missing, false)
	require.Equal(t, llm, got)
	require.NotContains(t, got, "Got it —")
	require.NotContains(t, got, "3 more after that")
}

func TestComposeReply_BlocksFalseReady(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{TargetJobTitle: "Nurse", PreferredCountries: []string{"KE"}}
	missing := []string{"capabilities", "job_types", "salary_expectation", "experience_level"}
	got := composeReply("You're all set — pick a plan!", f, missing, false)
	require.NotContains(t, strings.ToLower(got), "pick a plan")
	require.NotEmpty(t, got)
}

func TestComposeReply_EmptyFallsBackToGuided(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{
		PreferredCountries: []string{"ZA"},
		ExtraInfo:          "Uploaded CV on file. Resume document stored for matching (experience, education, skills).",
	}
	missing := []string{"target_job_title"}
	got := composeReply("", f, missing, false)
	require.Contains(t, got, "Got it —")
	require.Contains(t, strings.ToLower(got), "role")
}

func TestComposeReply_ReadyUsesLLM(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{TargetJobTitle: "PM", ExperienceLevel: "mid", PreferredCountries: []string{"KE"}}
	got := composeReply("You're ready — open Pricing when you want matches.", f, nil, true)
	require.Contains(t, got, "Pricing")
}
