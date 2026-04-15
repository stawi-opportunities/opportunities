package quality_test

import (
	"strings"
	"testing"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/quality"
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

func TestCheck_MissingCompany_Empty(t *testing.T) {
	j := validJob()
	j.Company = ""
	if err := quality.Check(j); err != quality.ErrMissingCompany {
		t.Fatalf("expected ErrMissingCompany, got: %v", err)
	}
}

func TestCheck_MissingCompany_SingleChar(t *testing.T) {
	j := validJob()
	j.Company = "X" // exactly 1 char — must fail (> 1 required)
	if err := quality.Check(j); err != quality.ErrMissingCompany {
		t.Fatalf("expected ErrMissingCompany for single-char company, got: %v", err)
	}
}

func TestCheck_MissingLocation_Empty(t *testing.T) {
	j := validJob()
	j.LocationText = ""
	if err := quality.Check(j); err != quality.ErrMissingLocation {
		t.Fatalf("expected ErrMissingLocation, got: %v", err)
	}
}

func TestCheck_MissingLocation_WhitespaceOnly(t *testing.T) {
	j := validJob()
	j.LocationText = "   "
	if err := quality.Check(j); err != quality.ErrMissingLocation {
		t.Fatalf("expected ErrMissingLocation for whitespace location, got: %v", err)
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

func TestCheck_MissingApplyURL_Empty(t *testing.T) {
	j := validJob()
	j.ApplyURL = ""
	if err := quality.Check(j); err != quality.ErrMissingApplyURL {
		t.Fatalf("expected ErrMissingApplyURL, got: %v", err)
	}
}

func TestCheck_InvalidApplyURL_NoScheme(t *testing.T) {
	j := validJob()
	j.ApplyURL = "example.com/apply"
	if err := quality.Check(j); err != quality.ErrInvalidApplyURL {
		t.Fatalf("expected ErrInvalidApplyURL for URL without scheme, got: %v", err)
	}
}

func TestCheck_InvalidApplyURL_FTPScheme(t *testing.T) {
	j := validJob()
	j.ApplyURL = "ftp://example.com/apply"
	if err := quality.Check(j); err != quality.ErrInvalidApplyURL {
		t.Fatalf("expected ErrInvalidApplyURL for ftp:// URL, got: %v", err)
	}
}

func TestCheck_ValidApplyURL_HTTP(t *testing.T) {
	j := validJob()
	j.ApplyURL = "http://example.com/apply"
	if err := quality.Check(j); err != nil {
		t.Fatalf("expected nil error for http:// URL, got: %v", err)
	}
}

func TestCheck_ValidApplyURL_HTTPS(t *testing.T) {
	j := validJob()
	j.ApplyURL = "https://example.com/apply"
	if err := quality.Check(j); err != nil {
		t.Fatalf("expected nil error for https:// URL, got: %v", err)
	}
}
