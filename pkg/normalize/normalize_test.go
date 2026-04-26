package normalize

import (
	"strings"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// TestExternalToVariant verifies the main conversion: trimming, company
// normalisation, country/currency casing, content hash, hard key and that
// the sourceBoard value does not panic.
func TestExternalToVariant(t *testing.T) {
	scrapedAt := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)

	ext := domain.ExternalJob{
		ExternalID:     "  job-123  ",
		Title:          "  Software Engineer  ",
		Company:        "  Acme Corp Ltd  ",
		LocationText:   "  Nairobi, Kenya  ",
		Description:    "  Great role.\x00  Exciting team.  ",
		ApplyURL:       "  https://apply.example.com  ",
		SourceURL:      "  https://source.example.com  ",
		RemoteType:     "REMOTE",
		EmploymentType: "FULL_TIME",
		Currency:       "kes",
		SalaryMin:      50000,
		SalaryMax:      80000,
	}

	v := ExternalToVariant(ext, "src_test_42", "ke", "brightermonday", "en", scrapedAt)

	// Trimming
	if v.Title != "Software Engineer" {
		t.Errorf("title not trimmed: got %q", v.Title)
	}
	if v.LocationText != "Nairobi, Kenya" {
		t.Errorf("location not trimmed: got %q", v.LocationText)
	}
	if v.ApplyURL != "https://apply.example.com" {
		t.Errorf("apply_url not trimmed: got %q", v.ApplyURL)
	}
	if v.SourceURL != "https://source.example.com" {
		t.Errorf("source_url not trimmed: got %q", v.SourceURL)
	}

	// Company normalisation (suffix stripped + trimmed)
	if v.Company != "Acme Corp" {
		t.Errorf("company not normalised: got %q", v.Company)
	}

	// Country to UPPER
	if v.Country != "KE" {
		t.Errorf("country not uppercased: got %q", v.Country)
	}

	// Currency to UPPER
	if v.Currency != "KES" {
		t.Errorf("currency not uppercased: got %q", v.Currency)
	}

	// RemoteType to lower
	if v.RemoteType != "remote" {
		t.Errorf("remote_type not lowercased: got %q", v.RemoteType)
	}

	// EmploymentType to lower
	if v.EmploymentType != "full_time" {
		t.Errorf("employment_type not lowercased: got %q", v.EmploymentType)
	}

	// Description: null bytes removed, whitespace collapsed
	if strings.Contains(v.Description, "\x00") {
		t.Error("description still contains null bytes")
	}
	if strings.Contains(v.Description, "  ") {
		t.Errorf("description has double spaces: %q", v.Description)
	}

	// Content hash must be a 64-char hex string
	if len(v.ContentHash) != 64 {
		t.Errorf("content hash length wrong: got %d", len(v.ContentHash))
	}

	// Hard key must be a 64-char hex string
	if len(v.HardKey) != 64 {
		t.Errorf("hard key length wrong: got %d", len(v.HardKey))
	}

	// ExternalJobID matches trimmed input
	if v.ExternalJobID != "job-123" {
		t.Errorf("external_job_id wrong: got %q", v.ExternalJobID)
	}

	// SourceID passed through
	if v.SourceID != "src_test_42" {
		t.Errorf("source_id wrong: got %q", v.SourceID)
	}

	// ScrapedAt passed through
	if !v.ScrapedAt.Equal(scrapedAt) {
		t.Errorf("scraped_at wrong: got %v", v.ScrapedAt)
	}

	// Salary fields passed through
	if v.SalaryMin != 50000 || v.SalaryMax != 80000 {
		t.Errorf("salary wrong: min=%v max=%v", v.SalaryMin, v.SalaryMax)
	}
}

// TestGeneratedIDWhenMissing verifies that an empty ExternalID is replaced by
// the first 16 characters of the content hash.
func TestGeneratedIDWhenMissing(t *testing.T) {
	ext := domain.ExternalJob{
		ExternalID:  "",
		Title:       "Data Analyst",
		Company:     "BigCo",
		LocationText: "Lagos",
		Description: "Analyse data.",
	}

	v := ExternalToVariant(ext, "src_test_1", "NG", "jobberman", "en", time.Now())

	if v.ExternalJobID == "" {
		t.Fatal("ExternalJobID should not be empty when input ExternalID is blank")
	}
	if len(v.ExternalJobID) != 16 {
		t.Errorf("generated ExternalJobID should be 16 chars, got %d: %q", len(v.ExternalJobID), v.ExternalJobID)
	}
	// The generated ID should be a prefix of the content hash.
	if !strings.HasPrefix(v.ContentHash, v.ExternalJobID) {
		t.Errorf("generated ID %q is not a prefix of content hash %q", v.ExternalJobID, v.ContentHash)
	}
}

// TestLanguageDetection covers the language-resolution rule: short text
// inherits the source language, long text overrides from whatlanggo, blank
// fallback becomes "en".
func TestLanguageDetection(t *testing.T) {
	shortEN := "Hiring engineer."
	longFR := "Nous recrutons un ingénieur logiciel senior pour rejoindre notre équipe à Paris. " +
		"Le poste est basé dans le 9e arrondissement avec télétravail partiel possible. " +
		"Vous serez responsable de la conception et du développement de services financiers. " +
		"Nous cherchons quelqu'un avec une solide expérience en systèmes distribués et infrastructure. " +
		"Des connaissances en français sont indispensables pour communiquer avec nos équipes locales."

	cases := []struct {
		name     string
		text     string
		fallback string
		want     string
	}{
		{"short text inherits fallback", shortEN, "fr", "fr"},
		{"blank fallback becomes en", shortEN, "", "en"},
		{"long text overrides fallback", longFR, "en", "fr"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectLanguage(tc.text, tc.fallback)
			if got != tc.want {
				t.Errorf("detectLanguage(%q, %q) = %q, want %q", tc.text[:min(40, len(tc.text))], tc.fallback, got, tc.want)
			}
		})
	}
}

// TestExternalToVariantLanguage verifies Language is populated on the
// resulting JobVariant, preferring detected over declared on long text.
func TestExternalToVariantLanguage(t *testing.T) {
	longFR := "Nous recrutons un ingénieur logiciel senior pour rejoindre notre équipe à Paris. " +
		"Le poste est basé dans le 9e arrondissement avec télétravail partiel possible. " +
		"Vous serez responsable de la conception et du développement de services financiers. " +
		"Nous cherchons quelqu'un avec une solide expérience en systèmes distribués."
	ext := domain.ExternalJob{
		ExternalID:  "fr-1",
		Title:       "Ingénieur",
		Company:     "ACME",
		Description: longFR,
	}
	v := ExternalToVariant(ext, "src_test_1", "FR", "greenhouse", "en", time.Now())
	if v.Language != "fr" {
		t.Errorf("Language = %q, want %q (whatlanggo should override)", v.Language, "fr")
	}

	vShort := ExternalToVariant(domain.ExternalJob{ExternalID: "j", Title: "Dev", Company: "ACME", Description: "Short"},
		"src_test_1", "FR", "greenhouse", "fr", time.Now())
	if vShort.Language != "fr" {
		t.Errorf("Language = %q, want %q (short text should inherit source)", vShort.Language, "fr")
	}
}

// TestCompanyNormalization is a table-driven test for normalizeCompany().
func TestCompanyNormalization(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Acme Corp Ltd", "Acme Corp"},
		{"Google Inc.", "Google"},
		{"Safaricom Pty", "Safaricom"},
		{"MTN Group GmbH", "MTN Group"},
		{"MicroSoft Corp.", "MicroSoft"},
		{"FooBar LLC", "FooBar"},
		{"Baz PLC", "Baz"},
		{"Quux Limited", "Quux"},
		{"Qux Incorporated", "Qux"},
		{"NoSuffix Co", "NoSuffix Co"},
		{"  Padded Ltd  ", "Padded"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeCompany(tc.input)
			if got != tc.want {
				t.Errorf("normalizeCompany(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestDetectRegion is a table-driven test for DetectRegion().
func TestDetectRegion(t *testing.T) {
	cases := []struct {
		country string
		want    string
	}{
		{"KE", "east_africa"},
		{"UG", "east_africa"},
		{"TZ", "east_africa"},
		{"RW", "east_africa"},
		{"ET", "east_africa"},
		{"SO", "east_africa"},
		{"NG", "west_africa"},
		{"GH", "west_africa"},
		{"ZA", "southern_africa"},
		{"ZW", "southern_africa"},
		{"EG", "north_africa"},
		{"MA", "north_africa"},
		{"GB", "europe"},
		{"DE", "europe"},
		{"AU", "oceania"},
		{"NZ", "oceania"},
		{"US", "americas"},
		{"CA", "americas"},
		{"IN", "asia"},
		{"SG", "asia"},
		// Lower-case input should still work
		{"ke", "east_africa"},
		{"ng", "west_africa"},
		// Unknown country
		{"XX", ""},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.country, func(t *testing.T) {
			got := DetectRegion(tc.country)
			if got != tc.want {
				t.Errorf("DetectRegion(%q) = %q; want %q", tc.country, got, tc.want)
			}
		})
	}
}
