package quality_test

import (
	"strings"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/quality"
)

// validJob returns a fully populated job that passes all quality checks.
func validJob() domain.ExternalJob {
	return domain.ExternalJob{
		Title:        "Senior Software Engineer",
		Company:      "Acme Corp",
		LocationText: "Nairobi, Kenya",
		Description:  strings.Repeat("x", 51),
		ApplyURL:     "https://example.com/apply",
	}
}

func TestCheck_ValidJob(t *testing.T) {
	if err := quality.Check(validJob()); err != nil {
		t.Fatalf("expected nil error for valid job, got: %v", err)
	}
}

func TestCheck_MissingTitle_Empty(t *testing.T) {
	j := validJob()
	j.Title = ""
	if err := quality.Check(j); err != quality.ErrMissingTitle {
		t.Fatalf("expected ErrMissingTitle, got: %v", err)
	}
}

func TestCheck_MissingTitle_TooShort(t *testing.T) {
	j := validJob()
	j.Title = "Dev" // exactly 3 chars — must fail (> 3 required)
	if err := quality.Check(j); err != quality.ErrMissingTitle {
		t.Fatalf("expected ErrMissingTitle for 3-char title, got: %v", err)
	}
}

func TestCheck_MissingTitle_WhitespaceOnly(t *testing.T) {
	j := validJob()
	j.Title = "   "
	if err := quality.Check(j); err != quality.ErrMissingTitle {
		t.Fatalf("expected ErrMissingTitle for whitespace title, got: %v", err)
	}
}

func TestCheck_ValidTitle_MinLength(t *testing.T) {
	j := validJob()
	j.Title = "Go Dev" // 6 chars — should pass
	if err := quality.Check(j); err != nil {
		t.Fatalf("expected nil error for 6-char title, got: %v", err)
	}
}

func TestCheck_ShortDescription_Exactly50Chars(t *testing.T) {
	j := validJob()
	j.Description = strings.Repeat("x", 50) // exactly 50 — must fail (> 50 required)
	if err := quality.Check(j); err != quality.ErrShortDescription {
		t.Fatalf("expected ErrShortDescription for 50-char description, got: %v", err)
	}
}

func TestCheck_ShortDescription_Empty(t *testing.T) {
	j := validJob()
	j.Description = ""
	if err := quality.Check(j); err != quality.ErrShortDescription {
		t.Fatalf("expected ErrShortDescription for empty description, got: %v", err)
	}
}

func TestCheck_ValidDescription_51Chars(t *testing.T) {
	j := validJob()
	j.Description = strings.Repeat("x", 51)
	if err := quality.Check(j); err != nil {
		t.Fatalf("expected nil error for 51-char description, got: %v", err)
	}
}

// TestCheck_MissingCompanyPasses verifies that a job with no company still passes.
func TestCheck_MissingCompanyPasses(t *testing.T) {
	j := validJob()
	j.Company = ""
	if err := quality.Check(j); err != nil {
		t.Fatalf("expected nil error for missing company, got: %v", err)
	}
}

// TestCheck_MissingLocationPasses verifies that a job with no location still passes.
func TestCheck_MissingLocationPasses(t *testing.T) {
	j := validJob()
	j.LocationText = ""
	if err := quality.Check(j); err != nil {
		t.Fatalf("expected nil error for missing location, got: %v", err)
	}
}

// TestEnsureApplyURL verifies that EnsureApplyURL sets a fallback when empty
// and leaves an existing URL untouched.
func TestEnsureApplyURL(t *testing.T) {
	t.Run("sets fallback when empty", func(t *testing.T) {
		j := domain.ExternalJob{ApplyURL: ""}
		quality.EnsureApplyURL(&j, "https://fallback.example.com")
		if j.ApplyURL != "https://fallback.example.com" {
			t.Fatalf("expected fallback URL to be set, got: %q", j.ApplyURL)
		}
	})

	t.Run("leaves existing URL alone", func(t *testing.T) {
		j := domain.ExternalJob{ApplyURL: "https://original.example.com/apply"}
		quality.EnsureApplyURL(&j, "https://fallback.example.com")
		if j.ApplyURL != "https://original.example.com/apply" {
			t.Fatalf("expected original URL to be preserved, got: %q", j.ApplyURL)
		}
	})

	t.Run("does not set empty fallback", func(t *testing.T) {
		j := domain.ExternalJob{ApplyURL: ""}
		quality.EnsureApplyURL(&j, "")
		if j.ApplyURL != "" {
			t.Fatalf("expected URL to remain empty when fallback is empty, got: %q", j.ApplyURL)
		}
	})

	t.Run("sets fallback when whitespace only", func(t *testing.T) {
		j := domain.ExternalJob{ApplyURL: "   "}
		quality.EnsureApplyURL(&j, "https://fallback.example.com")
		if j.ApplyURL != "https://fallback.example.com" {
			t.Fatalf("expected fallback URL to be set for whitespace-only ApplyURL, got: %q", j.ApplyURL)
		}
	})
}
