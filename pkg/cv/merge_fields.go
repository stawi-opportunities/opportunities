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
		out.Emails = append([]string(nil), out.Emails...)
		if out.WorkHistory != nil {
			wh := make([]map[string]any, len(out.WorkHistory))
			copy(wh, out.WorkHistory)
			out.WorkHistory = wh
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

	// Contact / name: prefer AI, fall back to heuristic.
	name := ""
	phone := ""
	var phones, emails []string
	if extracted != nil {
		name = extracted.Name
		phone = extracted.Phone
		phones = append([]string{}, extracted.Phones...)
		if len(phones) == 0 && phone != "" {
			phones = []string{phone}
		}
		emails = append([]string{}, extracted.Emails...)
		if len(emails) == 0 && extracted.Email != "" {
			emails = []string{extracted.Email}
		}
	}
	if name == "" {
		name = contact.Name
	}
	if phone == "" {
		phone = contact.Phone
	}
	phones = unionStrings(phones, contact.Phones)
	if phone != "" {
		phones = unionStrings([]string{phone}, phones)
	}
	emails = unionStrings(emails, contact.Emails)
	if contact.Email != "" {
		emails = unionStrings([]string{contact.Email}, emails)
	}

	fillStr(&out.Name, name, "name")

	// Phones: store all as " · " joined in Phone for display/edit.
	if len(phones) > 0 {
		joined := strings.Join(phones, " · ")
		if strings.TrimSpace(out.Phone) == "" {
			out.Phone = joined
			filled = append(filled, "phone")
		} else {
			// Expand existing single phone with any new numbers.
			existingPhones := splitContactList(out.Phone)
			mergedPhones := unionStrings(existingPhones, phones)
			if len(mergedPhones) > len(existingPhones) {
				out.Phone = strings.Join(mergedPhones, " · ")
				filled = append(filled, "phone")
			}
		}
	}

	if len(emails) > 0 {
		if len(out.Emails) == 0 {
			out.Emails = emails
			filled = append(filled, "emails")
		} else {
			before := len(out.Emails)
			out.Emails = unionStrings(out.Emails, emails)
			if len(out.Emails) > before {
				filled = append(filled, "emails")
			}
		}
	}

	// Heuristic section bodies from raw CV text.
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
	// Education: prefer longer multi-line education.
	fillStrPreferLonger(&out.Education, extracted.Education, "education", 20)
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

func splitContactList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Split on middle-dot, pipe, semicolon, or newlines — not bare spaces
	// (phone numbers contain spaces).
	parts := regexpSplitContacts(s)
	return cleanStrings(parts)
}

func regexpSplitContacts(s string) []string {
	// Reuse a simple splitter without importing regexp here again — use FieldsFunc.
	var parts []string
	var b strings.Builder
	flush := func() {
		p := strings.TrimSpace(b.String())
		if p != "" {
			parts = append(parts, p)
		}
		b.Reset()
	}
	for _, r := range s {
		switch r {
		case '·', '|', ';', '\n', '\r', '/':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	if len(parts) == 0 {
		return []string{s}
	}
	return parts
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
