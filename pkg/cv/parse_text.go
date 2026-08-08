package cv

import (
	"regexp"
	"strings"
	"unicode"
)

// ParsedContact holds contact details scraped from raw CV text without LLM.
type ParsedContact struct {
	Name  string
	Email string
	Phone string
}

var (
	emailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	// Loose international phone: optional +, digits/spaces/dashes, 8–15 digits total.
	phoneRE = regexp.MustCompile(`(?i)(?:\+|00)?[\d][\d\s().\-]{7,18}\d`)
)

// ParseContactFromText extracts name/email/phone from plain CV text with
// heuristics. Used as a baseline when AI extract is unavailable or incomplete.
func ParseContactFromText(text string) ParsedContact {
	text = strings.TrimSpace(text)
	if text == "" {
		return ParsedContact{}
	}
	out := ParsedContact{}
	if m := emailRE.FindString(text); m != "" {
		out.Email = strings.ToLower(strings.TrimSpace(m))
	}
	// Prefer phones near the top of the document.
	head := text
	if len(head) > 1200 {
		head = head[:1200]
	}
	for _, m := range phoneRE.FindAllString(head, -1) {
		digits := countDigits(m)
		if digits < 8 || digits > 15 {
			continue
		}
		// Skip year-like runs and pure short IDs.
		if strings.Contains(m, "@") {
			continue
		}
		out.Phone = normalizePhone(m)
		break
	}
	out.Name = guessName(text)
	return out
}

func countDigits(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			n++
		}
	}
	return n
}

func normalizePhone(s string) string {
	s = strings.TrimSpace(s)
	// Collapse internal whitespace runs.
	return strings.Join(strings.Fields(s), " ")
}

// guessName picks a likely full name from the first few non-empty lines.
func guessName(text string) string {
	lines := strings.Split(text, "\n")
	checked := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		checked++
		if checked > 8 {
			break
		}
		// Skip obvious non-name lines.
		lower := strings.ToLower(line)
		if strings.Contains(line, "@") || strings.Contains(lower, "http") {
			continue
		}
		if strings.HasPrefix(lower, "curriculum") || strings.HasPrefix(lower, "resume") ||
			strings.HasPrefix(lower, "cv ") || lower == "cv" || strings.HasPrefix(lower, "phone") ||
			strings.HasPrefix(lower, "email") || strings.HasPrefix(lower, "address") {
			continue
		}
		// Prefer 2–4 capitalised words, short line.
		words := strings.Fields(line)
		if len(words) < 2 || len(words) > 5 {
			continue
		}
		if len(line) > 60 {
			continue
		}
		if looksLikePersonName(words) {
			return strings.Join(words, " ")
		}
	}
	return ""
}

func looksLikePersonName(words []string) bool {
	alpha := 0
	for _, w := range words {
		clean := strings.Trim(w, ".,;")
		if clean == "" {
			return false
		}
		// Reject lines with digits (except roman numerals rare).
		for _, r := range clean {
			if unicode.IsDigit(r) {
				return false
			}
		}
		// First letter letter.
		runes := []rune(clean)
		if !unicode.IsLetter(runes[0]) {
			return false
		}
		alpha++
	}
	return alpha >= 2
}
