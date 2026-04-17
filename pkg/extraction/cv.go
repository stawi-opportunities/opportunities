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
type CVFields struct {
	Name               string            `json:"name"`
	Email              string            `json:"email"`
	Phone              string            `json:"phone"`
	Location           string            `json:"location"`
	CurrentTitle       string            `json:"current_title"`
	Bio                string            `json:"bio"`
	Seniority          string            `json:"seniority"`
	YearsExperience    int               `json:"years_experience"`
	PrimaryIndustry    string            `json:"primary_industry"`
	StrongSkills       []string          `json:"strong_skills"`
	WorkingSkills      []string          `json:"working_skills"`
	ToolsFrameworks    []string          `json:"tools_frameworks"`
	Certifications     []string          `json:"certifications"`
	PreferredRoles     []string          `json:"preferred_roles"`
	Languages          []string          `json:"languages"`
	Education          string            `json:"education"`
	WorkHistory        []WorkHistoryEntry `json:"work_history"`
	PreferredLocations []string          `json:"preferred_locations"`
	RemotePreference   string            `json:"remote_preference"`
	SalaryMin          string            `json:"salary_min"`
	SalaryMax          string            `json:"salary_max"`
	Currency           string            `json:"currency"`
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

const cvSystemPrompt = `You are a CV/resume data extractor. Output ONLY valid JSON. If a field is not found, use "" for strings, [] for arrays, 0 for numbers.

Extract these fields from the CV text below:

Personal: name, email, phone, location (city/country)

Profile: current_title (most recent job title), seniority ("intern"/"junior"/"mid"/"senior"/"lead"/"manager"/"director"/"executive"), years_experience (integer estimate), primary_industry, bio (2-3 sentence professional summary you write based on the CV)

Skills classification:
- strong_skills: skills mentioned across multiple roles OR accompanied by depth indicators like "led", "architected", "built", "designed", "owned", "expert"
- working_skills: skills mentioned only once or in passing
- tools_frameworks: specific technologies, tools, libraries, frameworks, platforms

Other: certifications (array), preferred_roles (array of role types the candidate targets), languages (array with proficiency if stated e.g. "English (native)")

Education: education (highest degree + institution as a single string)

Work history: work_history array, each entry: company, title, start_date (YYYY-MM or year), end_date (YYYY-MM, year, or "present"), summary (1-2 sentences)

Preferences (only if explicitly stated): preferred_locations (array), remote_preference ("remote"/"hybrid"/"onsite"/""), salary_min (number string), salary_max (number string), currency`

const maxCVChars = 6000

// ExtractCV sends CV plain text to the LLM and returns structured profile fields.
func (e *Extractor) ExtractCV(ctx context.Context, cvText string) (*CVFields, error) {
	text := truncateText(cvText, maxCVChars)
	prompt := fmt.Sprintf("%s\n\nCV text:\n%s", cvSystemPrompt, text)
	content, err := e.chat(ctx, prompt, true)
	if err != nil {
		return nil, fmt.Errorf("cv: %w", err)
	}
	return parseCVResponse(content)
}

// parseCVResponse unmarshals the JSON string produced by the model into CVFields.
func parseCVResponse(raw string) (*CVFields, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("cv: empty response from model")
	}
	var fields CVFields
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("cv: unmarshal cv fields: %w", err)
	}
	return &fields, nil
}
