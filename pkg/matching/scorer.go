package matching

import (
	"strings"
	"time"
)

// ComputeMatchScore returns a weighted composite score in [0, 1] from the six
// sub-scores.  Weights: skills 35%, embedding 25%, quality 15%, salary 10%,
// recency 10%, seniority 5%.
func ComputeMatchScore(skillsOverlap, embeddingSimilarity, qualityScore, salaryFit, recency, seniorityFit float64) float64 {
	return 0.35*skillsOverlap +
		0.25*embeddingSimilarity +
		0.15*(qualityScore/100.0) +
		0.10*salaryFit +
		0.10*recency +
		0.05*seniorityFit
}

// SkillsOverlap computes Jaccard similarity between two comma-separated skill
// strings.  Tokens are trimmed, lowercased, and de-duplicated before the set
// operations are applied.
func SkillsOverlap(candidateSkills, jobRequiredSkills string) float64 {
	a := tokenSet(candidateSkills)
	b := tokenSet(jobRequiredSkills)
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// tokenSet splits a comma-separated string into a set of trimmed, lowercased
// non-empty tokens.
func tokenSet(s string) map[string]bool {
	parts := strings.Split(s, ",")
	set := make(map[string]bool, len(parts))
	for _, p := range parts {
		if t := strings.ToLower(strings.TrimSpace(p)); t != "" {
			set[t] = true
		}
	}
	return set
}

// SalaryFit returns a score in [0, 1] representing how well the candidate's
// salary expectation overlaps with the job's advertised range.
//
//   - 1.0  ranges overlap
//   - 0.5  candidate has no salary preference (both zero)
//   - 0.3  job has no salary information
//   - gap-decay  candidate min > job max: 1 / (1 + gap/jobMax)
func SalaryFit(candidateMin, candidateMax, jobMin, jobMax float64) float64 {
	// No candidate preference — accept anything.
	if candidateMin == 0 && candidateMax == 0 {
		return 0.5
	}
	// Job has no salary data.
	if jobMin == 0 && jobMax == 0 {
		return 0.3
	}
	// Resolve effective bounds (treat 0 as "unbounded").
	effCandMin := candidateMin
	effCandMax := candidateMax
	if effCandMax == 0 {
		effCandMax = effCandMin
	}
	effJobMin := jobMin
	effJobMax := jobMax
	if effJobMax == 0 {
		effJobMax = effJobMin
	}

	// Overlapping ranges.
	if effCandMin <= effJobMax && effJobMin <= effCandMax {
		return 1.0
	}

	// Candidate expects more than job offers — decay by gap.
	if effCandMin > effJobMax && effJobMax > 0 {
		gap := effCandMin - effJobMax
		return 1.0 / (1.0 + gap/effJobMax)
	}

	return 0.0
}

// Recency returns 1.0 if the job was seen today, decaying linearly to 0.0 at
// 30 days old.  Jobs older than 30 days score 0.
func Recency(jobLastSeen time.Time) float64 {
	age := time.Since(jobLastSeen).Hours() / 24.0
	if age <= 0 {
		return 1.0
	}
	if age >= 30.0 {
		return 0.0
	}
	return 1.0 - age/30.0
}

// seniorityLevel maps a seniority label to a numeric level.  Unknown labels
// map to -1 (treated as "no data").
var seniorityLevel = map[string]int{
	"intern":    0,
	"junior":    1,
	"mid":       2,
	"senior":    3,
	"lead":      4,
	"manager":   5,
	"director":  6,
	"executive": 7,
}

// SeniorityFit returns 1.0 for exact match, 0.5 for ±1 level, and 0.0 for
// everything else (including unknown / empty labels).
func SeniorityFit(candidateSeniority, jobSeniority string) float64 {
	cLvl, cOK := seniorityLevel[strings.ToLower(strings.TrimSpace(candidateSeniority))]
	jLvl, jOK := seniorityLevel[strings.ToLower(strings.TrimSpace(jobSeniority))]
	if !cOK || !jOK {
		return 0.0
	}
	diff := cLvl - jLvl
	if diff < 0 {
		diff = -diff
	}
	switch diff {
	case 0:
		return 1.0
	case 1:
		return 0.5
	default:
		return 0.0
	}
}
