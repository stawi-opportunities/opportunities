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
	// Meta answer already steers toward role — keep as-is (no append).
	missing := []string{"target_job_title", "job_types", "salary_expectation", "experience_level"}
	llm := "Yes — this chat is onboarding. I already see a CV and ZA markets; next I need a concrete role title so we can score jobs fairly."
	got := composeReply(llm, f, missing, false)
	require.Equal(t, llm, got)
	require.NotContains(t, got, "Got it —")
	require.NotContains(t, got, "3 more after that")
}

func TestComposeReply_AppendsNextAskWhenLLMDoesNotSteer(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{
		PreferredCountries: []string{"ZA"},
		ExtraInfo:          "Uploaded CV on file. Resume document stored for matching (experience, education, skills).",
	}
	missing := []string{"target_job_title", "job_types"}
	llm := "Happy to help — tell me more about what you're looking for whenever you're ready."
	got := composeReply(llm, f, missing, false)
	require.Contains(t, got, llm)
	require.Contains(t, got, "What role should we match you to")
	require.NotContains(t, got, "Got it —")
}

func TestComposeReply_BlocksFalseReady(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{TargetJobTitle: "Nurse", PreferredCountries: []string{"KE"}}
	missing := []string{"capabilities", "job_types", "salary_expectation", "experience_level"}
	got := composeReply("You're all set — pick a plan!", f, missing, false)
	require.NotContains(t, strings.ToLower(got), "pick a plan")
	require.NotEmpty(t, got)
}

func TestComposeReply_BlocksPlacementProfileCompleteClaim(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{
		TargetJobTitle:     "Senior Software Developer",
		PreferredCountries: []string{"UG"},
		ExtraInfo:          "Uploaded CV on file. Resume document stored for matching (experience, education, skills).",
		JobTypes:           []string{"Full-time"},
		ExperienceLevel:    "senior",
	}
	missing := []string{"salary_expectation"}
	llm := "Got it—I've updated your location preference to Uganda for remote Software Developer roles. Your placement profile is complete, and we are ready to match you with suitable opportunities."
	got := composeReply(llm, f, missing, false)
	require.NotContains(t, strings.ToLower(got), "profile is complete")
	require.NotContains(t, strings.ToLower(got), "ready to match")
	require.Contains(t, strings.ToLower(got), "salary")
}

func TestAISalaryExpectationSatisfiesReadiness(t *testing.T) {
	t.Parallel()
	// AI free-text salary signal (not phrase-list matching on user text).
	f := onboardingChatFields{
		TargetJobTitle:     "Senior Software Developer",
		PreferredCountries: []string{"UG"},
		JobTypes:           []string{"Full-time"},
		ExperienceLevel:    "senior",
		ExtraInfo:          "Uploaded CV on file. Resume document stored for matching (experience, education, skills).",
		SalaryExpectation:  "open / market rates, no hard limits",
	}
	require.True(t, hasSalaryExpectation(f))
	require.Equal(t, "open / market rates, no hard limits", formatSalary(f))
	st := assessFieldStatus(f)
	require.True(t, st["salary_expectation"].OK)
	require.Empty(t, missingFromStatus(st))
}

func TestApplyComposedReplyToMessages_OverwritesLastAssistant(t *testing.T) {
	t.Parallel()
	msgs := []onboardingChatMessage{
		{Role: "user", Content: "I want remote roles from Uganda"},
		{Role: "assistant", Content: "Your placement profile is complete!"},
	}
	out := applyComposedReplyToMessages(msgs, "What are your salary expectations? (e.g. USD 80k–120k, or KES 200000+)")
	require.Len(t, out, 2)
	require.Equal(t, "user", out[0].Role)
	require.Equal(t, "assistant", out[1].Role)
	require.Contains(t, out[1].Content, "salary")
	require.NotContains(t, out[1].Content, "complete")
}

func TestComposeReply_EmptyDoesNotInventGuided(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{
		PreferredCountries: []string{"ZA"},
		ExtraInfo:          "Uploaded CV on file. Resume document stored for matching (experience, education, skills).",
	}
	missing := []string{"target_job_title"}
	got := composeReply("", f, missing, false)
	require.Empty(t, got)
	require.NotContains(t, got, "Got it —")
}

func TestComposeReply_ReadyUsesLLM(t *testing.T) {
	t.Parallel()
	f := onboardingChatFields{TargetJobTitle: "PM", ExperienceLevel: "mid", PreferredCountries: []string{"KE"}}
	got := composeReply("You're ready — open Pricing when you want matches.", f, nil, true)
	require.Contains(t, got, "Pricing")
}
