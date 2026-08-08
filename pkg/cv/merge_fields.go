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
// Returns the merged bag and the list of field keys that were filled.
func MergeExtractedIntoProfile(
	existing *candidatestore.ProfileFields,
	extracted *extraction.CVFields,
	contact ParsedContact,
) (merged *candidatestore.ProfileFields, filled []string) {
	out := &candidatestore.ProfileFields{}
	if existing != nil {
		*out = *existing
	}
	filled = make([]string, 0, 16)

	fillStr := func(cur *string, val, key string) {
		val = strings.TrimSpace(val)
		if val == "" || strings.TrimSpace(*cur) != "" {
			return
		}
		*cur = val
		filled = append(filled, key)
	}
	fillSlice := func(cur *[]string, val []string, key string) {
		if len(val) == 0 || len(*cur) > 0 {
			return
		}
		clean := make([]string, 0, len(val))
		for _, s := range val {
			s = strings.TrimSpace(s)
			if s != "" {
				clean = append(clean, s)
			}
		}
		if len(clean) == 0 {
			return
		}
		*cur = clean
		filled = append(filled, key)
	}

	// Contact / name: prefer AI, fall back to heuristic.
	name := ""
	phone := ""
	if extracted != nil {
		name = extracted.Name
		phone = extracted.Phone
	}
	if name == "" {
		name = contact.Name
	}
	if phone == "" {
		phone = contact.Phone
	}
	fillStr(&out.Name, name, "name")
	fillStr(&out.Phone, phone, "phone")

	if extracted == nil {
		return out, filled
	}

	fillStr(&out.CurrentTitle, extracted.CurrentTitle, "current_title")
	fillStr(&out.Bio, extracted.Bio, "bio")
	fillStr(&out.Seniority, extracted.Seniority, "seniority")
	fillStr(&out.Education, extracted.Education, "education")
	fillStr(&out.RemotePref, extracted.RemotePreference, "remote_preference")
	fillStr(&out.Currency, extracted.Currency, "currency")

	if out.YearsExperience == 0 && extracted.YearsExperience > 0 {
		out.YearsExperience = extracted.YearsExperience
		filled = append(filled, "years_experience")
	}
	if extracted.PrimaryIndustry != "" && len(out.Industries) == 0 {
		out.Industries = []string{extracted.PrimaryIndustry}
		filled = append(filled, "industries")
	}

	fillSlice(&out.StrongSkills, extracted.StrongSkills, "strong_skills")
	fillSlice(&out.WorkingSkills, extracted.WorkingSkills, "working_skills")
	fillSlice(&out.ToolsFrameworks, extracted.ToolsFrameworks, "tools_frameworks")
	fillSlice(&out.Certifications, extracted.Certifications, "certifications")
	fillSlice(&out.PreferredRoles, extracted.PreferredRoles, "preferred_roles")
	fillSlice(&out.Languages, extracted.Languages, "languages")
	fillSlice(&out.Locations, extracted.PreferredLocations, "preferred_locations")

	// Skills bag used by matchers.
	if len(out.Skills) == 0 {
		skills := append([]string{}, out.StrongSkills...)
		skills = append(skills, out.WorkingSkills...)
		if len(skills) > 0 {
			out.Skills = skills
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

	// Work history — only when empty.
	if len(out.WorkHistory) == 0 && len(extracted.WorkHistory) > 0 {
		wh := make([]map[string]any, 0, len(extracted.WorkHistory))
		for _, e := range extracted.WorkHistory {
			row := map[string]any{
				"company":     e.Company,
				"title":       e.Title,
				"start":       e.StartDate,
				"end":         e.EndDate,
				"description": e.Summary,
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
