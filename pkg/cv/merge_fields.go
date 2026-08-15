package cv

import (
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/candidatestore"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// MergeExtractedIntoProfile fills only empty / zero fields on existing with
// values from AI extract + optional heuristic contact. Never overwrites
// user-entered non-empty data.
//
// rawCV is the full plain-text CV (optional). When provided, heuristic
// summary / skills / certs sections fill gaps the model missed.
//
// Returns the merged bag and the list of field keys that were filled.
func MergeExtractedIntoProfile(
	existing *candidatestore.ProfileFields,
	extracted *extraction.CVFields,
	contact ParsedContact,
) (merged *candidatestore.ProfileFields, filled []string) {
	return MergeExtractedIntoProfileWithText(existing, extracted, contact, "")
}

// MergeExtractedIntoProfileWithText is the full merge path including raw CV
// text for section heuristics.
func MergeExtractedIntoProfileWithText(
	existing *candidatestore.ProfileFields,
	extracted *extraction.CVFields,
	contact ParsedContact,
	rawCV string,
) (merged *candidatestore.ProfileFields, filled []string) {
	out := &candidatestore.ProfileFields{}
	if existing != nil {
		*out = *existing
		// Deep-copy slices so we never mutate the caller's bag.
		out.Skills = append([]string(nil), out.Skills...)
		out.StrongSkills = append([]string(nil), out.StrongSkills...)
		out.WorkingSkills = append([]string(nil), out.WorkingSkills...)
		out.ToolsFrameworks = append([]string(nil), out.ToolsFrameworks...)
		out.Certifications = append([]string(nil), out.Certifications...)
		out.PreferredRoles = append([]string(nil), out.PreferredRoles...)
		out.Industries = append([]string(nil), out.Industries...)
		out.Languages = append([]string(nil), out.Languages...)
		out.Locations = append([]string(nil), out.Locations...)
		out.Countries = append([]string(nil), out.Countries...)
		out.Regions = append([]string(nil), out.Regions...)
		out.Timezones = append([]string(nil), out.Timezones...)
		if out.WorkHistory != nil {
			wh := make([]map[string]any, len(out.WorkHistory))
			copy(wh, out.WorkHistory)
			out.WorkHistory = wh
		}
		if out.EducationHistory != nil {
			eh := make([]map[string]any, len(out.EducationHistory))
			copy(eh, out.EducationHistory)
			out.EducationHistory = eh
		}
	}
	filled = make([]string, 0, 24)

	fillStr := func(cur *string, val, key string) {
		val = strings.TrimSpace(val)
		if val == "" || strings.TrimSpace(*cur) != "" {
			return
		}
		*cur = val
		filled = append(filled, key)
	}
	// fillStrPreferLonger fills empty slots, or upgrades a short stub when
	// the new value is substantially longer (about/bio recovery).
	fillStrPreferLonger := func(cur *string, val, key string, minUpgrade int) {
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}
		curTrim := strings.TrimSpace(*cur)
		if curTrim == "" {
			*cur = val
			filled = append(filled, key)
			return
		}
		// Upgrade truncated/stub about when new text is much richer.
		if minUpgrade > 0 && len([]rune(val)) >= minUpgrade &&
			len([]rune(val)) > len([]rune(curTrim))*2 {
			*cur = val
			filled = append(filled, key)
		}
	}
	fillSlice := func(cur *[]string, val []string, key string) {
		if len(val) == 0 {
			return
		}
		if len(*cur) > 0 {
			// Merge missing items into existing rather than skip entirely.
			before := len(*cur)
			*cur = unionStrings(*cur, val)
			if len(*cur) > before {
				filled = append(filled, key)
			}
			return
		}
		clean := cleanStrings(val)
		if len(clean) == 0 {
			return
		}
		*cur = clean
		filled = append(filled, key)
	}

	// Name only from AI/heuristic. Contact details (email/phone) are NOT
	// stored on candidate_profiles — they live exclusively in platform
	// ProfileService via profilecontacts.Ensure (secure single store).
	name := ""
	if extracted != nil {
		name = extracted.Name
	}
	if name == "" {
		name = contact.Name
	}
	fillStr(&out.Name, name, "name")

	// Heuristic section bodies from raw CV text (bio/skills/certs only —
	// education structure comes from AI education_history, not local parsers).
	summarySection := ExtractSummarySection(rawCV)
	skillsSection := ExtractSkillsSection(rawCV)
	certsSection := ExtractCertificationsSection(rawCV)
	skillTokens := SplitSkillTokens(skillsSection)
	certTokens := SplitSkillTokens(certsSection)

	if extracted == nil {
		// Still fill bio / skills / certs from heuristics alone.
		fillStrPreferLonger(&out.Bio, summarySection, "bio", 40)
		fillSlice(&out.StrongSkills, skillTokens, "strong_skills")
		fillSlice(&out.Certifications, certTokens, "certifications")
		if len(out.Skills) == 0 && len(out.StrongSkills) > 0 {
			out.Skills = append([]string{}, out.StrongSkills...)
			filled = append(filled, "skills")
		}
		return out, filled
	}

	fillStr(&out.CurrentTitle, extracted.CurrentTitle, "current_title")

	// About: prefer verbatim CV summary section, else model bio.
	bio := strings.TrimSpace(extracted.Bio)
	if summarySection != "" {
		// Prefer the longer of model bio vs section when section looks complete.
		if len([]rune(summarySection)) >= len([]rune(bio)) {
			bio = summarySection
		}
	}
	fillStrPreferLonger(&out.Bio, bio, "bio", 40)

	fillStr(&out.Seniority, extracted.Seniority, "seniority")
	// Education: AI-structured education_history only (no local free-text parse).
	mergeEducationFromAI(out, extracted, &filled)
	fillStr(&out.RemotePref, extracted.RemotePreference, "remote_preference")
	fillStr(&out.Currency, extracted.Currency, "currency")

	// Location from CV → preferred_locations when empty.
	if extracted.Location != "" {
		fillSlice(&out.Locations, []string{extracted.Location}, "preferred_locations")
	}

	if out.YearsExperience == 0 && extracted.YearsExperience > 0 {
		out.YearsExperience = extracted.YearsExperience
		filled = append(filled, "years_experience")
	}
	if extracted.PrimaryIndustry != "" && len(out.Industries) == 0 {
		out.Industries = []string{extracted.PrimaryIndustry}
		filled = append(filled, "industries")
	}

	// Skills: model first, then union heuristic tokens so nothing on the CV is lost.
	fillSlice(&out.StrongSkills, extracted.StrongSkills, "strong_skills")
	fillSlice(&out.WorkingSkills, extracted.WorkingSkills, "working_skills")
	fillSlice(&out.ToolsFrameworks, extracted.ToolsFrameworks, "tools_frameworks")
	if len(skillTokens) > 0 {
		// Put unmatched heuristic skills into working if strong already set.
		if len(out.StrongSkills) == 0 {
			fillSlice(&out.StrongSkills, skillTokens, "strong_skills")
		} else {
			// Anything not already classified → working skills.
			extra := difference(skillTokens, unionStrings(out.StrongSkills, out.WorkingSkills, out.ToolsFrameworks))
			fillSlice(&out.WorkingSkills, extra, "working_skills")
		}
	}

	fillSlice(&out.Certifications, extracted.Certifications, "certifications")
	if len(certTokens) > 0 {
		fillSlice(&out.Certifications, certTokens, "certifications")
	}

	fillSlice(&out.PreferredRoles, extracted.PreferredRoles, "preferred_roles")
	fillSlice(&out.Languages, extracted.Languages, "languages")
	fillSlice(&out.Locations, extracted.PreferredLocations, "preferred_locations")

	// Skills bag used by matchers.
	if len(out.Skills) == 0 {
		skills := append([]string{}, out.StrongSkills...)
		skills = append(skills, out.WorkingSkills...)
		skills = append(skills, out.ToolsFrameworks...)
		if len(skills) > 0 {
			out.Skills = skills
			filled = append(filled, "skills")
		}
	} else {
		before := len(out.Skills)
		out.Skills = unionStrings(out.Skills, out.StrongSkills, out.WorkingSkills, out.ToolsFrameworks)
		if len(out.Skills) > before {
			filled = append(filled, "skills")
		}
	}

	// Experience level from seniority when missing.
	if out.ExperienceLevel == "" && out.Seniority != "" {
		out.ExperienceLevel = mapSeniorityToLevel(out.Seniority)
		if out.ExperienceLevel != "" {
			filled = append(filled, "experience_level")
		}
	}

	// Work history — only when empty; keep full summaries from extract.
	if len(out.WorkHistory) == 0 && len(extracted.WorkHistory) > 0 {
		wh := make([]map[string]any, 0, len(extracted.WorkHistory))
		for _, e := range extracted.WorkHistory {
			row := map[string]any{
				"company":     e.Company,
				"title":       e.Title,
				"start":       e.StartDate,
				"end":         e.EndDate,
				"description": e.Summary,
				"summary":     e.Summary,
			}
			if strings.EqualFold(strings.TrimSpace(e.EndDate), "present") ||
				strings.EqualFold(strings.TrimSpace(e.EndDate), "current") {
				row["current"] = true
			}
			wh = append(wh, row)
		}
		out.WorkHistory = wh
		filled = append(filled, "work_history")
	}

	// Target role from preferred roles or current title when blank.
	if out.TargetJobTitle == "" {
		if len(extracted.PreferredRoles) > 0 {
			out.TargetJobTitle = extracted.PreferredRoles[0]
			filled = append(filled, "target_job_title")
		} else if out.CurrentTitle != "" {
			out.TargetJobTitle = out.CurrentTitle
			filled = append(filled, "target_job_title")
		}
	}

	return out, filled
}

// mergeEducationFromAI stores structured education from the model only.
// We do not regex-parse free-text into school/degree — that is the AI's job.
func mergeEducationFromAI(
	out *candidatestore.ProfileFields,
	extracted *extraction.CVFields,
	filled *[]string,
) {
	if out == nil || extracted == nil {
		return
	}
	// Respect user-edited structured history.
	if len(out.EducationHistory) > 0 {
		if strings.TrimSpace(out.Education) == "" {
			if summary := extraction.FormatEducationSummary(mapsToEducationEntries(out.EducationHistory)); summary != "" {
				out.Education = summary
				*filled = append(*filled, "education")
			}
		}
		return
	}

	if len(extracted.EducationHistory) > 0 {
		out.EducationHistory = educationEntriesToMaps(extracted.EducationHistory)
		*filled = append(*filled, "education_history")
		summary := strings.TrimSpace(extracted.Education)
		if summary == "" {
			summary = extraction.FormatEducationSummary(extracted.EducationHistory)
		}
		if summary != "" {
			out.Education = summary
			*filled = append(*filled, "education")
		}
		return
	}

	// Model returned only free-text education (older prompt / incomplete JSON).
	// Keep the text for search; do not invent structured rows in code.
	edu := strings.TrimSpace(extracted.Education)
	if edu == "" {
		return
	}
	if strings.TrimSpace(out.Education) == "" {
		out.Education = edu
		*filled = append(*filled, "education")
	} else if len([]rune(edu)) > len([]rune(out.Education))*2 && len([]rune(edu)) >= 20 {
		out.Education = edu
		*filled = append(*filled, "education")
	}
}

func educationEntriesToMaps(entries []extraction.EducationEntry) []map[string]any {
	if len(entries) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if e.School == "" && e.Degree == "" && e.Field == "" {
			continue
		}
		out = append(out, map[string]any{
			"school":     e.School,
			"degree":     e.Degree,
			"field":      e.Field,
			"start":      e.StartDate,
			"end":        e.EndDate,
			"start_date": e.StartDate,
			"end_date":   e.EndDate,
			"notes":      e.Notes,
		})
	}
	return out
}

func mapsToEducationEntries(rows []map[string]any) []extraction.EducationEntry {
	if len(rows) == 0 {
		return nil
	}
	out := make([]extraction.EducationEntry, 0, len(rows))
	for _, r := range rows {
		school, _ := r["school"].(string)
		degree, _ := r["degree"].(string)
		field, _ := r["field"].(string)
		start, _ := r["start_date"].(string)
		if start == "" {
			start, _ = r["start"].(string)
		}
		end, _ := r["end_date"].(string)
		if end == "" {
			end, _ = r["end"].(string)
		}
		notes, _ := r["notes"].(string)
		if strings.TrimSpace(school) == "" && strings.TrimSpace(degree) == "" && strings.TrimSpace(field) == "" {
			continue
		}
		out = append(out, extraction.EducationEntry{
			School:    strings.TrimSpace(school),
			Degree:    strings.TrimSpace(degree),
			Field:     strings.TrimSpace(field),
			StartDate: strings.TrimSpace(start),
			EndDate:   strings.TrimSpace(end),
			Notes:     strings.TrimSpace(notes),
		})
	}
	return out
}

func cleanStrings(val []string) []string {
	clean := make([]string, 0, len(val))
	seen := map[string]struct{}{}
	for _, s := range val {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		k := strings.ToLower(s)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		clean = append(clean, s)
	}
	return clean
}

func unionStrings(parts ...[]string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, list := range parts {
		for _, s := range list {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			k := strings.ToLower(s)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func difference(from []string, remove []string) []string {
	drop := map[string]struct{}{}
	for _, r := range remove {
		drop[strings.ToLower(strings.TrimSpace(r))] = struct{}{}
	}
	var out []string
	for _, s := range from {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := drop[strings.ToLower(s)]; ok {
			continue
		}
		out = append(out, s)
	}
	return out
}

func mapSeniorityToLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "intern", "internship":
		return "intern"
	case "junior", "entry", "entry-level":
		return "junior"
	case "mid", "middle", "intermediate":
		return "mid"
	case "senior", "sr", "sr.":
		return "senior"
	case "lead", "staff", "principal":
		return "lead"
	case "manager", "eng manager":
		return "manager"
	case "director", "head":
		return "director"
	case "executive", "vp", "c-level", "cxo":
		return "executive"
	default:
		return s
	}
}
