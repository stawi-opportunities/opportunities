package cv

import (
	"regexp"
	"strings"
	"unicode"
)

// ParsedContact holds contact details scraped from raw CV text without LLM.
type ParsedContact struct {
	Name   string
	Email  string   // primary
	Emails []string // all
	Phone  string   // primary
	Phones []string // all
}

var (
	emailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	// Loose international phone: optional +, digits/spaces/dashes, 8–15 digits total.
	phoneRE = regexp.MustCompile(`(?i)(?:\+|00)?[\d][\d\s().\-]{7,18}\d`)
	// Section headers often used for summary / skills / certs.
	summaryHeaderRE = regexp.MustCompile(`(?im)^\s*(professional\s+summary|summary|profile|about(\s+me)?|objective|career\s+summary|personal\s+statement)\s*:?\s*$`)
	skillsHeaderRE  = regexp.MustCompile(`(?im)^\s*(skills|technical\s+skills|core\s+competenc(y|ies)|key\s+skills|competencies|expertise)\s*:?\s*$`)
	certsHeaderRE   = regexp.MustCompile(`(?im)^\s*(certifications?|licen[cs]es?|credentials|professional\s+certifications?)\s*:?\s*$`)
	// Next major section that ends a block.
	nextSectionRE = regexp.MustCompile(`(?im)^\s*(experience|work\s+experience|employment|education|skills|technical\s+skills|certifications?|projects|languages?|interests|references|publications|awards|summary|profile|about|objective)\s*:?\s*$`)
)

// ParseContactFromText extracts name/email/phone from plain CV text with
// heuristics. Used as a baseline when AI extract is unavailable or incomplete.
func ParseContactFromText(text string) ParsedContact {
	text = strings.TrimSpace(text)
	if text == "" {
		return ParsedContact{}
	}
	out := ParsedContact{}

	for _, m := range emailRE.FindAllString(text, -1) {
		em := strings.ToLower(strings.TrimSpace(m))
		if em == "" {
			continue
		}
		out.Emails = appendUniqueFold(out.Emails, em)
	}
	if len(out.Emails) > 0 {
		out.Email = out.Emails[0]
	}

	// Prefer phones near the top of the document, then whole doc.
	scanPhone := func(chunk string) {
		for _, m := range phoneRE.FindAllString(chunk, -1) {
			digits := countDigits(m)
			if digits < 8 || digits > 15 {
				continue
			}
			if strings.Contains(m, "@") {
				continue
			}
			// Skip pure years / long numeric IDs without separators when too short span.
			p := normalizePhone(m)
			if p == "" {
				continue
			}
			out.Phones = appendUniqueFold(out.Phones, p)
		}
	}
	head := text
	if len(head) > 2500 {
		head = head[:2500]
	}
	scanPhone(head)
	if len(out.Phones) == 0 {
		scanPhone(text)
	}
	if len(out.Phones) > 0 {
		out.Phone = out.Phones[0]
	}

	out.Name = guessName(text)
	return out
}

// ExtractSummarySection returns verbatim Summary/Profile/About body when present.
func ExtractSummarySection(text string) string {
	return sectionBody(text, summaryHeaderRE)
}

// ExtractSkillsSection returns raw skills section text (comma/line items).
func ExtractSkillsSection(text string) string {
	return sectionBody(text, skillsHeaderRE)
}

// ExtractCertificationsSection returns raw certifications section text.
func ExtractCertificationsSection(text string) string {
	return sectionBody(text, certsHeaderRE)
}

// SplitSkillTokens splits a skills section into individual tokens.
func SplitSkillTokens(section string) []string {
	section = strings.TrimSpace(section)
	if section == "" {
		return nil
	}
	// Prefer newlines / bullets; also split on commas and pipes / middle dots.
	raw := regexp.MustCompile(`[\n•·|;/]+`).Split(section, -1)
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		// Further split long comma lists.
		for _, part := range strings.Split(p, ",") {
			part = strings.TrimSpace(part)
			part = strings.Trim(part, "·-–—*•\t ")
			if part == "" || len(part) > 80 {
				continue
			}
			// Skip section-like leftovers.
			lower := strings.ToLower(part)
			if lower == "skills" || lower == "technical skills" {
				continue
			}
			out = appendUniqueFold(out, part)
		}
	}
	return out
}

func sectionBody(text string, header *regexp.Regexp) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	// Work line-oriented so multi-line sections survive.
	lines := strings.Split(text, "\n")
	// If the PDF extractor collapsed to few newlines, also try on a
	// loosely re-wrapped version.
	if len(lines) < 3 && len(text) > 200 {
		// Insert breaks before common ALL-CAPS headers.
		loose := regexp.MustCompile(`(?i)\s+(PROFESSIONAL SUMMARY|SUMMARY|PROFILE|ABOUT ME|OBJECTIVE|EXPERIENCE|WORK EXPERIENCE|EDUCATION|SKILLS|CERTIFICATIONS|LANGUAGES)\s+`)
		text2 := loose.ReplaceAllString(text, "\n$1\n")
		lines = strings.Split(text2, "\n")
	}

	start := -1
	for i, line := range lines {
		if header.MatchString(strings.TrimSpace(line)) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	var body []string
	for i := start; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if trim == "" {
			if len(body) > 0 {
				body = append(body, "")
			}
			continue
		}
		if nextSectionRE.MatchString(trim) && !header.MatchString(trim) {
			break
		}
		body = append(body, lines[i])
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
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

func appendUniqueFold(dst []string, val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return dst
	}
	low := strings.ToLower(val)
	for _, e := range dst {
		if strings.ToLower(e) == low {
			return dst
		}
	}
	return append(dst, val)
}

// guessName picks a likely full name from the first few non-empty lines,
// or from tokens before the first email on a single-line CV (common PDF dump).
func guessName(text string) string {
	if n := guessNameFromLines(text); n != "" {
		return n
	}
	return guessNameBeforeContact(text)
}

func guessNameFromLines(text string) string {
	lines := strings.Split(text, "\n")
	checked := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		checked++
		if checked > 12 {
			break
		}
		// Skip obvious non-name lines.
		lower := strings.ToLower(line)
		if strings.Contains(line, "@") || strings.Contains(lower, "http") {
			continue
		}
		if strings.HasPrefix(lower, "curriculum") || strings.HasPrefix(lower, "resume") ||
			strings.HasPrefix(lower, "cv ") || lower == "cv" || strings.HasPrefix(lower, "phone") ||
			strings.HasPrefix(lower, "email") || strings.HasPrefix(lower, "address") ||
			strings.HasPrefix(lower, "tel") || strings.HasPrefix(lower, "mobile") ||
			strings.HasPrefix(lower, "linkedin") {
			continue
		}
		// Prefer 2–5 capitalised words, short line.
		words := strings.Fields(line)
		if len(words) < 2 || len(words) > 6 {
			continue
		}
		if len(line) > 80 {
			continue
		}
		if looksLikePersonName(words) {
			return strings.Join(words, " ")
		}
	}
	return ""
}

// guessNameBeforeContact handles single-line / collapsed PDF text where the
// name is the first 2–4 words before an email or phone.
func guessNameBeforeContact(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Cut at first email or long digit run.
	cut := len(text)
	if loc := emailRE.FindStringIndex(text); loc != nil && loc[0] < cut {
		cut = loc[0]
	}
	if loc := phoneRE.FindStringIndex(text); loc != nil && loc[0] < cut && loc[0] > 0 {
		// Only if phone is early (header).
		if loc[0] < 120 {
			cut = loc[0]
		}
	}
	head := strings.TrimSpace(text[:cut])
	// Take first line-ish chunk.
	if i := strings.IndexAny(head, "\n|•"); i > 0 {
		head = head[:i]
	}
	words := strings.Fields(head)
	if len(words) < 2 {
		return ""
	}
	// Drop leading labels.
	for len(words) > 0 {
		w := strings.ToLower(strings.Trim(words[0], ":"))
		if w == "name" || w == "cv" || w == "resume" {
			words = words[1:]
			continue
		}
		break
	}
	if len(words) > 5 {
		words = words[:5]
	}
	if len(words) < 2 {
		return ""
	}
	// Reject if first token looks like a job title keyword.
	first := strings.ToLower(words[0])
	for _, bad := range []string{"senior", "junior", "software", "engineer", "developer", "manager", "director", "lead"} {
		if first == bad {
			return ""
		}
	}
	if !looksLikePersonName(words) {
		return ""
	}
	return strings.Join(words, " ")
}

func looksLikePersonName(words []string) bool {
	alpha := 0
	for _, w := range words {
		clean := strings.Trim(w, ".,;|")
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
