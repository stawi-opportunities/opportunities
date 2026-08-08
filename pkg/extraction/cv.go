package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/nguyenthenguyen/docx"
)

// CVFields holds the structured profile fields extracted from a CV.
// Arrays (emails, phones, skills, certifications, work_history) should be
// comprehensive — prefer completeness over brevity.
type CVFields struct {
	Name               string             `json:"name"`
	Email              string             `json:"email"`
	Emails             []string           `json:"emails"`
	Phone              string             `json:"phone"`
	Phones             []string           `json:"phones"`
	Location           string             `json:"location"`
	CurrentTitle       string             `json:"current_title"`
	Bio                string             `json:"bio"`
	Seniority          string             `json:"seniority"`
	YearsExperience    int                `json:"years_experience"`
	PrimaryIndustry    string             `json:"primary_industry"`
	StrongSkills       []string           `json:"strong_skills"`
	WorkingSkills      []string           `json:"working_skills"`
	ToolsFrameworks    []string           `json:"tools_frameworks"`
	Certifications     []string           `json:"certifications"`
	PreferredRoles     []string           `json:"preferred_roles"`
	Languages          []string           `json:"languages"`
	Education          string             `json:"education"`
	WorkHistory        []WorkHistoryEntry `json:"work_history"`
	PreferredLocations []string           `json:"preferred_locations"`
	RemotePreference   string             `json:"remote_preference"`
	SalaryMin          string             `json:"salary_min"`
	SalaryMax          string             `json:"salary_max"`
	Currency           string             `json:"currency"`
}

// WorkHistoryEntry represents a single position in a candidate's work history.
type WorkHistoryEntry struct {
	Company   string `json:"company"`
	Title     string `json:"title"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Summary   string `json:"summary"`
}

// ExtractTextFromPDF extracts plain text from PDF file bytes.
func ExtractTextFromPDF(data []byte) (string, error) {
	r := bytes.NewReader(data)
	reader, err := pdf.NewReader(r, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("cv: open pdf: %w", err)
	}

	plainText, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("cv: read pdf text: %w", err)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(plainText); err != nil {
		return "", fmt.Errorf("cv: read pdf buffer: %w", err)
	}

	return buf.String(), nil
}

// xmlTagRe strips XML/HTML tags for plain-text extraction from DOCX content.
var xmlTagRe = regexp.MustCompile(`<[^>]+>`)

// ExtractTextFromDOCX extracts plain text from DOCX file bytes.
func ExtractTextFromDOCX(data []byte) (string, error) {
	r := bytes.NewReader(data)
	doc, err := docx.ReadDocxFromMemory(r, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("cv: open docx: %w", err)
	}
	defer func() { _ = doc.Close() }()

	// GetContent returns the raw word/document.xml; strip tags to get text.
	raw := doc.Editable().GetContent()
	text := xmlTagRe.ReplaceAllString(raw, " ")

	// Decode common XML entities.
	text = strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
		"&nbsp;", " ",
	).Replace(text)

	// Collapse whitespace.
	wsRe := regexp.MustCompile(`\s+`)
	text = wsRe.ReplaceAllString(text, " ")

	return strings.TrimSpace(text), nil
}

// ExtractTextFromFile routes extraction by file extension.
// Supported extensions: .pdf, .docx, .txt (and plain fallback for unknown).
func ExtractTextFromFile(data []byte, filename string) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return ExtractTextFromPDF(data)
	case strings.HasSuffix(lower, ".docx"):
		return ExtractTextFromDOCX(data)
	case strings.HasSuffix(lower, ".txt"):
		return string(data), nil
	default:
		// Attempt plain text fallback for unknown types.
		return string(data), nil
	}
}

const cvSystemPrompt = `You are a meticulous CV/resume data extractor. Output ONLY valid JSON (no markdown fences, no prose).
If a field is not found use "" for strings, [] for arrays, 0 for numbers.

COMPLETENESS RULES (critical):
- Prefer the candidate's own wording. Do NOT invent employers, degrees, skills, or certs.
- Keep work_history summaries FULL: copy every bullet/responsibility/achievement for that role (use newlines between bullets). Do not compress to 1–2 sentences.
- bio: if the CV has a Summary / Profile / About / Objective section, copy that section VERBATIM (full text). Only synthesize a short bio when no such section exists.
- skills & certifications: be EXHAUSTIVE — include every skill, tool, library, platform, methodology, and certification mentioned anywhere (skills section, tools line, experience bullets, headers).
- emails and phones: include EVERY distinct email and phone number on the CV (not just the first).

Extract these fields:

Personal:
- name: full legal/preferred name as printed (usually top of CV)
- email: primary email (first or most professional-looking)
- emails: array of ALL emails found
- phone: primary phone
- phones: array of ALL phone numbers found (keep international formatting)
- location: city/region/country as stated

Profile:
- current_title: most recent or primary job title
- seniority: one of intern|junior|mid|senior|lead|manager|director|executive
- years_experience: integer estimate of total professional years
- primary_industry: main industry if clear
- bio: see completeness rules above

Skills classification (dedupe, preserve original casing where sensible):
- strong_skills: core strengths (repeated across roles, "expert", "led", "architected", "owned", listed under primary skills)
- working_skills: secondary skills / familiar with / mentioned once
- tools_frameworks: languages, frameworks, databases, cloud, devops, SaaS, libraries, platforms
- certifications: full names of licenses/certs (e.g. "AWS Solutions Architect – Associate", "PMP", "CPA") — not soft skills
- preferred_roles: target roles if stated
- languages: spoken/written languages with proficiency when stated

Education:
- education: all degrees/institutions as a multi-line string (one entry per line: Degree, Field — School (years))

Work history:
- work_history: array ordered most-recent first. Each entry:
  company, title, start_date (YYYY-MM or year), end_date (YYYY-MM, year, or "present"),
  summary = FULL role text (all bullets/paragraphs for that job)

Preferences (only if explicitly stated):
- preferred_locations, remote_preference (remote|hybrid|onsite|""), salary_min, salary_max, currency (number strings for salary)`

// maxCVChars bounds model input. Keep high so long CVs are not truncated;
// Gemini/large-context models handle full resumes.
const maxCVChars = 100_000

// ExtractCV sends CV plain text to the LLM and returns structured profile fields.
func (e *Extractor) ExtractCV(ctx context.Context, cvText string) (*CVFields, error) {
	text := truncateText(cvText, maxCVChars)
	prompt := fmt.Sprintf("%s\n\nCV text:\n%s", cvSystemPrompt, text)
	content, err := e.chat(ctx, prompt, true)
	if err != nil {
		return nil, fmt.Errorf("cv: %w", err)
	}
	fields, err := parseCVResponse(content)
	if err != nil {
		return nil, err
	}
	normalizeCVFields(fields)
	return fields, nil
}

// parseCVResponse unmarshals the JSON string produced by the model into CVFields.
func parseCVResponse(raw string) (*CVFields, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("cv: empty response from model")
	}
	// Strip accidental markdown fences.
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```JSON")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	var fields CVFields
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("cv: unmarshal cv fields: %w", err)
	}
	return &fields, nil
}

// normalizeCVFields dedupes contact arrays and backfills primary email/phone.
func normalizeCVFields(f *CVFields) {
	if f == nil {
		return
	}
	f.Name = strings.TrimSpace(f.Name)
	f.Email = strings.TrimSpace(strings.ToLower(f.Email))
	f.Phone = strings.TrimSpace(f.Phone)
	f.Bio = strings.TrimSpace(f.Bio)
	f.CurrentTitle = strings.TrimSpace(f.CurrentTitle)
	f.Location = strings.TrimSpace(f.Location)
	f.Education = strings.TrimSpace(f.Education)

	f.Emails = dedupeFold(append(f.Emails, f.Email), true)
	f.Phones = dedupeFold(append(f.Phones, f.Phone), false)
	if f.Email == "" && len(f.Emails) > 0 {
		f.Email = f.Emails[0]
	}
	if f.Phone == "" && len(f.Phones) > 0 {
		f.Phone = f.Phones[0]
	}

	f.StrongSkills = dedupeFold(f.StrongSkills, false)
	f.WorkingSkills = dedupeFold(f.WorkingSkills, false)
	f.ToolsFrameworks = dedupeFold(f.ToolsFrameworks, false)
	f.Certifications = dedupeFold(f.Certifications, false)
	f.Languages = dedupeFold(f.Languages, false)
	f.PreferredRoles = dedupeFold(f.PreferredRoles, false)
	f.PreferredLocations = dedupeFold(f.PreferredLocations, false)

	for i := range f.WorkHistory {
		f.WorkHistory[i].Company = strings.TrimSpace(f.WorkHistory[i].Company)
		f.WorkHistory[i].Title = strings.TrimSpace(f.WorkHistory[i].Title)
		f.WorkHistory[i].StartDate = strings.TrimSpace(f.WorkHistory[i].StartDate)
		f.WorkHistory[i].EndDate = strings.TrimSpace(f.WorkHistory[i].EndDate)
		f.WorkHistory[i].Summary = strings.TrimSpace(f.WorkHistory[i].Summary)
	}
}

func dedupeFold(in []string, lower bool) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if lower {
			s = strings.ToLower(s)
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
