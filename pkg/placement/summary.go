// Package placement builds the candidate "placement profile": a single
// dynamic document that combines qualifications (CV) and preferences
// (chat / onboard) for vector matching and agent guidance.
package placement

import (
	"fmt"
	"strings"
)

// Fields is the structured preference/qualification snapshot used by chat
// and matching. Mirrors onboarding chat fields without importing HTTP pkgs.
type Fields struct {
	TargetJobTitle     string
	ExperienceLevel    string
	JobSearchStatus    string
	SalaryMin          *float64
	SalaryMax          *float64
	Currency           string
	PreferredRegions   []string
	PreferredCountries []string
	PreferredTimezones []string
	PreferredLanguages []string
	JobTypes           []string
	Country            string
	LinkedIn           string
	// ExtraInfo / CVText is qualifications corpus (CV plain text).
	ExtraInfo string
}

// Document is the stored placement profile / matching persona.
type Document struct {
	CandidateID string
	Version     int
	// SummaryText is the full vectorizable document (query side).
	SummaryText string
	// QualificationsText is the CV / skills portion.
	QualificationsText string
	// PreferencesText is the wants / constraints portion.
	PreferencesText string
	// ConversationDigest is rolling chat intent for semantic matching.
	ConversationDigest string
	// RerankText is a short persona slice for Path A cross-encoder.
	RerankText string
	// ContentHash fingerprints SummaryText for embed skip.
	ContentHash string
	// Missing is required keys still incomplete for high-quality matching.
	Missing []string
	// Ready is true when all required matching signals are present.
	Ready bool
}

// RequiredFieldOrder is the guided collection order for the agent harness.
var RequiredFieldOrder = []string{
	"target_job_title",
	"capabilities",
	"job_types",
	"salary_expectation",
	"preferred_countries",
	"experience_level",
}

// FieldGuide explains why each required signal matters for placement.
var FieldGuide = map[string]struct {
	Label string
	Why   string
	Ask   string
}{
	"target_job_title": {
		Label: "Target role",
		Why:   "Matching scores opportunities against a concrete role title.",
		Ask:   "What role should we match you to? (e.g. Senior Software Engineer, Product Manager)",
	},
	"capabilities": {
		Label: "CV / qualifications",
		Why:   "Your resume is how we understand skills, experience depth, and fit.",
		Ask:   "Please attach or paste your CV so we can assess your capabilities.",
	},
	"job_types": {
		Label: "Job types",
		Why:   "We only notify you about employment kinds you care about.",
		Ask:   "What kinds of jobs should we notify you about? (Full-time, Part-time, Contract, Freelance, Internship)",
	},
	"salary_expectation": {
		Label: "Salary",
		Why:   "Salary filters avoid poor-fit listings and improve ranking.",
		Ask:   "What are your salary expectations? (e.g. USD 80k–120k, or KES 200000+)",
	},
	"preferred_countries": {
		Label: "Opportunity countries",
		Why:   "We source and rank opportunities from the markets you choose.",
		Ask:   "Which countries should we source opportunities from? (e.g. Kenya, Nigeria, remote US)",
	},
	"experience_level": {
		Label: "Experience level",
		Why:   "Level steers seniority-appropriate matches (entry through executive).",
		Ask:   "What's your experience level — entry, junior, mid, senior, lead, or executive?",
	},
}

// MissingRequired returns incomplete required keys in priority order.
func MissingRequired(f Fields) []string {
	var miss []string
	if strings.TrimSpace(f.TargetJobTitle) == "" {
		miss = append(miss, "target_job_title")
	}
	if !looksLikeCV(f.ExtraInfo) {
		miss = append(miss, "capabilities")
	}
	if len(nonEmpty(f.JobTypes)) == 0 {
		miss = append(miss, "job_types")
	}
	if !hasSalary(f) {
		miss = append(miss, "salary_expectation")
	}
	if len(nonEmpty(f.PreferredCountries)) == 0 && strings.TrimSpace(f.Country) == "" {
		miss = append(miss, "preferred_countries")
	}
	if strings.TrimSpace(f.ExperienceLevel) == "" {
		miss = append(miss, "experience_level")
	}
	return miss
}

// BuildDocument composes qualifications + preferences into one summary.
// Prefer BuildPersonaDocument when chat turns are available.
func BuildDocument(candidateID string, f Fields) Document {
	return BuildPersonaDocument(candidateID, f, nil, DefaultPersonaConfig())
}

// BuildPersonaDocument builds the matching persona from structured fields
// plus optional chat turns (conversation-grounded intent).
func BuildPersonaDocument(candidateID string, f Fields, turns []ChatTurn, cfg PersonaConfig) Document {
	if cfg.MaxRunes <= 0 {
		cfg = DefaultPersonaConfig()
	}
	qual := buildQualifications(f)
	prefs := buildPreferences(f)
	missing := MissingRequired(f)
	digest := ""
	if cfg.IncludeConversation && len(turns) > 0 {
		digest = BuildConversationDigest(turns, cfg.DigestMaxRunes)
	}
	summary := composePersonaSummary(qual, prefs, digest, f, missing)
	summary = truncateRunes(summary, cfg.MaxRunes)
	headline := matchHeadline(f)
	return Document{
		CandidateID:        candidateID,
		SummaryText:        summary,
		QualificationsText: qual,
		PreferencesText:    prefs,
		ConversationDigest: digest,
		RerankText:         RerankText(headline, prefs, digest, 1800),
		ContentHash:        ContentHash(summary),
		Missing:            missing,
		Ready:              len(missing) == 0,
	}
}

func buildQualifications(f Fields) string {
	var b strings.Builder
	b.WriteString("## Qualifications\n")
	cv := strings.TrimSpace(f.ExtraInfo)
	if looksLikeCV(cv) {
		// Cap for embedding token budget while keeping substance.
		b.WriteString(truncateRunes(cv, 6000))
		b.WriteByte('\n')
	} else if cv != "" {
		b.WriteString(truncateRunes(cv, 800))
		b.WriteByte('\n')
	} else {
		b.WriteString("(CV not yet provided)\n")
	}
	if li := strings.TrimSpace(f.LinkedIn); li != "" {
		b.WriteString("LinkedIn: ")
		b.WriteString(li)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func buildPreferences(f Fields) string {
	var lines []string
	lines = append(lines, "## Preferences (what they want)")
	if t := strings.TrimSpace(f.TargetJobTitle); t != "" {
		lines = append(lines, "Target role: "+t)
	}
	if e := strings.TrimSpace(f.ExperienceLevel); e != "" {
		lines = append(lines, "Experience level: "+e)
	}
	if types := nonEmpty(f.JobTypes); len(types) > 0 {
		lines = append(lines, "Job types: "+strings.Join(types, ", "))
	}
	if hasSalary(f) {
		lines = append(lines, "Salary expectation: "+formatSalary(f))
	}
	countries := nonEmpty(f.PreferredCountries)
	if len(countries) == 0 && strings.TrimSpace(f.Country) != "" {
		countries = []string{strings.TrimSpace(f.Country)}
	}
	if len(countries) > 0 {
		lines = append(lines, "Opportunity countries: "+strings.Join(countries, ", "))
	}
	if regions := nonEmpty(f.PreferredRegions); len(regions) > 0 {
		lines = append(lines, "Regions: "+strings.Join(regions, ", "))
	}
	if langs := nonEmpty(f.PreferredLanguages); len(langs) > 0 {
		lines = append(lines, "Languages: "+strings.Join(langs, ", "))
	}
	if st := strings.TrimSpace(f.JobSearchStatus); st != "" {
		lines = append(lines, "Search status: "+st)
	}
	if home := strings.TrimSpace(f.Country); home != "" {
		lines = append(lines, "Home/base country: "+home)
	}
	if len(lines) == 1 {
		lines = append(lines, "(Preferences not yet collected)")
	}
	return strings.Join(lines, "\n")
}

func composePersonaSummary(qual, prefs, digest string, f Fields, missing []string) string {
	var b strings.Builder
	b.WriteString("# Matching persona v1\n")
	b.WriteString("Unified document for opportunity matching: intent + preferences + CV.\n\n")
	b.WriteString("## Match headline\n")
	headline := matchHeadline(f)
	if headline != "" {
		b.WriteString(headline)
		b.WriteByte('\n')
	} else {
		b.WriteString("(incomplete profile)\n")
	}
	b.WriteByte('\n')
	if d := strings.TrimSpace(digest); d != "" {
		b.WriteString("## Intent (conversation-grounded)\n")
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	b.WriteString(prefs)
	b.WriteString("\n\n")
	b.WriteString(qual)
	if len(missing) > 0 {
		b.WriteString("\n\n## Still needed for quality matching\n")
		for _, k := range missing {
			g := FieldGuide[k]
			if g.Label != "" {
				b.WriteString("- ")
				b.WriteString(g.Label)
				b.WriteString(": ")
				b.WriteString(g.Why)
				b.WriteByte('\n')
			} else {
				b.WriteString("- ")
				b.WriteString(k)
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func matchHeadline(f Fields) string {
	var parts []string
	if t := strings.TrimSpace(f.TargetJobTitle); t != "" {
		parts = append(parts, t)
	}
	if e := strings.TrimSpace(f.ExperienceLevel); e != "" {
		parts = append(parts, e+" level")
	}
	if types := nonEmpty(f.JobTypes); len(types) > 0 {
		parts = append(parts, strings.Join(types, "/"))
	}
	if hasSalary(f) {
		parts = append(parts, formatSalary(f))
	}
	countries := nonEmpty(f.PreferredCountries)
	if len(countries) == 0 && strings.TrimSpace(f.Country) != "" {
		countries = []string{strings.TrimSpace(f.Country)}
	}
	if len(countries) > 0 {
		parts = append(parts, "markets "+strings.Join(countries, ", "))
	}
	if looksLikeCV(f.ExtraInfo) {
		parts = append(parts, "CV on file")
	}
	return strings.Join(parts, " · ")
}

// GuidedFollowUp builds an agent reply that acknowledges known signals and
// asks for the highest-priority missing field with why it matters.
func GuidedFollowUp(f Fields) string {
	doc := BuildDocument("", f)
	if doc.Ready {
		return fmt.Sprintf(
			"Great — your placement profile looks complete (%s). You can choose a plan to start matching.",
			matchHeadline(f),
		)
	}
	next := doc.Missing[0]
	g := FieldGuide[next]
	ack := ""
	if h := matchHeadline(f); h != "" {
		ack = "Got it — " + h + ". "
	}
	why := ""
	if g.Why != "" {
		why = " " + g.Why
	}
	ask := g.Ask
	if ask == "" {
		ask = "Could you share a bit more so we can match the right opportunities?"
	}
	remain := ""
	if len(doc.Missing) > 1 {
		remain = fmt.Sprintf(" (%d more after that.)", len(doc.Missing)-1)
	}
	return strings.TrimSpace(ack + ask + why + remain)
}

// IndexFilters extracts match-index filter fields from preferences.
type IndexFilters struct {
	Countries      []string
	Kinds          []string
	SalaryFloorUSD *int
	RemoteOnly     bool
}

// FiltersFromFields maps placement fields onto candidate_match_indexes filters.
func FiltersFromFields(f Fields) IndexFilters {
	countries := nonEmpty(f.PreferredCountries)
	if len(countries) == 0 && strings.TrimSpace(f.Country) != "" {
		countries = []string{strings.ToUpper(strings.TrimSpace(f.Country))}
	}
	// Normalize to upper ISO-ish codes when 2-letter.
	for i, c := range countries {
		c = strings.TrimSpace(c)
		if len(c) == 2 {
			countries[i] = strings.ToUpper(c)
		}
	}
	kinds := []string{"job"}
	// Job types don't map 1:1 to opportunity kinds; keep "job" for now.
	var floor *int
	if f.SalaryMin != nil && *f.SalaryMin > 0 {
		v := int(*f.SalaryMin)
		// Rough USD-ish floor; non-USD still used as relative filter when present.
		floor = &v
	} else if f.SalaryMax != nil && *f.SalaryMax > 0 {
		v := int(*f.SalaryMax * 0.7)
		floor = &v
	}
	return IndexFilters{
		Countries:      countries,
		Kinds:          kinds,
		SalaryFloorUSD: floor,
		RemoteOnly:     false,
	}
}

func hasSalary(f Fields) bool {
	return (f.SalaryMin != nil && *f.SalaryMin > 0) || (f.SalaryMax != nil && *f.SalaryMax > 0)
}

func formatSalary(f Fields) string {
	cur := f.Currency
	if cur == "" {
		cur = "USD"
	}
	switch {
	case f.SalaryMin != nil && f.SalaryMax != nil && *f.SalaryMin != *f.SalaryMax:
		return fmt.Sprintf("%s %.0f–%.0f", cur, *f.SalaryMin, *f.SalaryMax)
	case f.SalaryMin != nil:
		return fmt.Sprintf("%s %.0f+", cur, *f.SalaryMin)
	case f.SalaryMax != nil:
		return fmt.Sprintf("%s up to %.0f", cur, *f.SalaryMax)
	default:
		return ""
	}
}

func looksLikeCV(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 80 {
		return false
	}
	low := strings.ToLower(s)
	markers := 0
	for _, m := range []string{
		"experience", "education", "skills", "employment", "curriculum",
		"resume", "cv", "worked at", "responsibilities", "bachelor", "master",
		"mba", "bsc", "msc", "university", "years of experience",
	} {
		if strings.Contains(low, m) {
			markers++
		}
	}
	if markers >= 2 {
		return true
	}
	if markers >= 1 && len(s) >= 200 {
		return true
	}
	return false
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
