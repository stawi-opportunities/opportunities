package cv

import (
	"strings"
	"testing"
)

func TestParseContactFromText(t *testing.T) {
	text := `Jane A. Doe
Software Engineer
jane.doe@example.com
+254 712 345 678
Nairobi, Kenya

EXPERIENCE
...
`
	got := ParseContactFromText(text)
	if got.Email != "jane.doe@example.com" {
		t.Fatalf("email=%q", got.Email)
	}
	if got.Phone == "" {
		t.Fatal("expected phone")
	}
	if got.Name != "Jane A. Doe" {
		t.Fatalf("name=%q", got.Name)
	}
}

func TestParseMultiEmailsAndPhones(t *testing.T) {
	text := `Alex Kim
alex@work.com | home@personal.org
Mobile: +1 (415) 555-0100
Office: +1 415 555 0199
`
	got := ParseContactFromText(text)
	if len(got.Emails) < 2 {
		t.Fatalf("emails=%v", got.Emails)
	}
	if len(got.Phones) < 1 {
		t.Fatalf("phones=%v", got.Phones)
	}
	if got.Name == "" {
		t.Fatal("expected name")
	}
}

func TestParseNameFromCollapsedLine(t *testing.T) {
	// PDF dumps often collapse to a single line.
	text := `Mary Wanjiku mw@example.com +254700111222 Nairobi Software Engineer EXPERIENCE Acme Corp`
	got := ParseContactFromText(text)
	if !strings.Contains(strings.ToLower(got.Name), "mary") {
		t.Fatalf("name=%q emails=%v", got.Name, got.Emails)
	}
	if got.Email != "mw@example.com" {
		t.Fatalf("email=%q", got.Email)
	}
}

func TestExtractSummaryAndSkillsSections(t *testing.T) {
	text := `Name Here

SUMMARY
I build reliable distributed systems for fintech.

SKILLS
Go, Rust, PostgreSQL, Kafka

CERTIFICATIONS
CKA
AWS Developer Associate

EXPERIENCE
Something
`
	sum := ExtractSummarySection(text)
	if !strings.Contains(sum, "distributed systems") {
		t.Fatalf("summary=%q", sum)
	}
	skills := SplitSkillTokens(ExtractSkillsSection(text))
	if len(skills) < 3 {
		t.Fatalf("skills=%v", skills)
	}
	certs := SplitSkillTokens(ExtractCertificationsSection(text))
	if len(certs) < 2 {
		t.Fatalf("certs=%v", certs)
	}
}
