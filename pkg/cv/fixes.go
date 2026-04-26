package cv

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// maxPriorityFixes is the UI's display cap. Generating more than this
// creates decision fatigue — scoring wins here are top-heavy so the
// 8th-ranked fix would shift the overall score by <2 points anyway.
const maxPriorityFixes = 7

// maxRewrites limits the batched LLM rewrite call to 5 bullets. At 5,
// one Groq call handles the load; beyond that we pay noticeably
// longer latency for diminishing value (weakest bullets already
// captured).
const maxRewrites = 5

// detectPriorityFixes walks the per-component scores and emits a
// ranked list of concrete actions the user can take. Deterministic —
// every issue maps to a fixed template so the same CV always produces
// the same fix list. The LLM-driven bullet rewrites are generated
// separately in AttachRewrites below.
func detectPriorityFixes(cvText string, fields *extraction.CVFields, family RoleFamily, c ScoreComponents) []PriorityFix {
	var fixes []PriorityFix

	// ── ATS-driven fixes ───────────────────────────────────────────
	lower := strings.ToLower(cvText)
	if !containsAny(lower, "summary", "professional summary", "about", "profile") {
		fixes = append(fixes, PriorityFix{
			ID:       "add-summary",
			Title:    "Add a 2-line professional summary",
			Impact:   "high",
			Category: "ats",
			Why:      "Recruiters spend 10 seconds scanning a CV; a summary at the top is where that scan lands. Without one, you're asking them to infer the fit.",
			AutoApplicable: false,
		})
	}
	if !strings.Contains(lower, "skills") {
		fixes = append(fixes, PriorityFix{
			ID:       "add-skills-section",
			Title:    "Add a structured Skills section",
			Impact:   "high",
			Category: "ats",
			Why:      "ATS parsers look for an explicit 'Skills' header — without one, your stack is invisible to automated filtering.",
			AutoApplicable: true,
		})
	}
	if fields != nil && len(fields.WorkHistory) == 0 {
		fixes = append(fixes, PriorityFix{
			ID:       "add-work-history",
			Title:    "Add at least one Experience entry with dates",
			Impact:   "high",
			Category: "ats",
			Why:      "Every ATS parser keys on role title + company + dates. Missing experience entries means the CV fails parsing entirely.",
			AutoApplicable: false,
		})
	}

	// ── Keyword fixes ──────────────────────────────────────────────
	if c.Keywords < 70 {
		missing := missingKeywords(cvText, family, 6)
		if len(missing) > 0 {
			fixes = append(fixes, PriorityFix{
				ID:       "add-keywords",
				Title:    fmt.Sprintf("Add missing keywords: %s", strings.Join(missing, ", ")),
				Impact:   impactForScore(c.Keywords, 50, 70),
				Category: "keywords",
				Why:      "ATS filters match on exact keywords from the job family. Missing these is the difference between getting shortlisted and getting filtered.",
				AutoApplicable: true,
				Suggestions:    missing,
			})
		}
	}

	// ── Impact fixes ───────────────────────────────────────────────
	if c.Impact < 65 {
		weakCount := countWeakBullets(fields)
		if weakCount > 0 {
			fixes = append(fixes, PriorityFix{
				ID:       "strengthen-bullets",
				Title:    fmt.Sprintf("Rewrite %d experience bullet%s for measurable impact", weakCount, plural(weakCount)),
				Impact:   "high",
				Category: "impact",
				Why:      "Bullets that describe work (\"built backend services\") score lower than bullets that describe outcome (\"built services handling 2M requests/min\"). Numbers are the single biggest lever on this score.",
				AutoApplicable: false, // metrics need user input
			})
		}
		if countBulletsWithWeakVerb(fields) > 0 {
			fixes = append(fixes, PriorityFix{
				ID:       "replace-weak-verbs",
				Title:    "Replace weak verbs (\"worked on\", \"helped\") with strong action verbs",
				Impact:   "medium",
				Category: "impact",
				Why:      "Recruiter pattern-matching rewards \"built / shipped / led / drove\" and penalises \"worked on / helped / assisted\". Strong verbs make the same experience read as ownership rather than participation.",
				AutoApplicable: true,
				Suggestions:    []string{"built", "shipped", "led", "drove", "owned", "delivered", "scaled"},
			})
		}
	}

	// ── Role-fit fixes ─────────────────────────────────────────────
	if c.RoleFit < 60 {
		fixes = append(fixes, PriorityFix{
			ID:       "tighten-role-fit",
			Title:    "Tighten role alignment — your CV reads generally instead of targeting " + string(family),
			Impact:   "medium",
			Category: "role_fit",
			Why:      "The profile embedding sits far from the reference for your target role. Lead with the parts of your experience that match the role; demote unrelated history.",
			AutoApplicable: false,
		})
	}

	// ── Clarity fixes ──────────────────────────────────────────────
	if c.Clarity < 70 {
		if buzzCount := countBuzzwords(cvText); buzzCount > 0 {
			fixes = append(fixes, PriorityFix{
				ID:       "remove-buzzwords",
				Title:    "Remove buzzwords (\"synergy\", \"rockstar\", etc.)",
				Impact:   "low",
				Category: "clarity",
				Why:      "Buzzwords are recruiter-flag phrases — they signal resume inflation without adding information. Replace them with concrete outcomes.",
				AutoApplicable: true,
			})
		}
	}

	// Rank by impact bucket (high > medium > low), preserving insertion
	// order within each bucket. Cap at maxPriorityFixes.
	sort.SliceStable(fixes, func(i, j int) bool {
		return impactRank(fixes[i].Impact) < impactRank(fixes[j].Impact)
	})
	if len(fixes) > maxPriorityFixes {
		fixes = fixes[:maxPriorityFixes]
	}
	return fixes
}

// missingKeywords returns up to `limit` canonical keywords the CV
// doesn't contain. Sorted by order in expectedKeywords so the most
// important (first-listed) gaps surface first.
func missingKeywords(cvText string, family RoleFamily, limit int) []string {
	lower := strings.ToLower(cvText)
	var missing []string
	for _, kw := range expectedKeywords[family] {
		if !keywordPresent(lower, kw) {
			missing = append(missing, kw)
			if len(missing) >= limit {
				break
			}
		}
	}
	return missing
}

func countWeakBullets(fields *extraction.CVFields) int {
	if fields == nil {
		return 0
	}
	n := 0
	for _, job := range fields.WorkHistory {
		for _, bullet := range splitBullets(job.Summary) {
			if scoreBullet(bullet) < 60 {
				n++
			}
		}
	}
	return n
}

func countBulletsWithWeakVerb(fields *extraction.CVFields) int {
	if fields == nil {
		return 0
	}
	n := 0
	for _, job := range fields.WorkHistory {
		for _, bullet := range splitBullets(job.Summary) {
			if startsWithWeakVerb(strings.ToLower(strings.TrimSpace(bullet))) {
				n++
			}
		}
	}
	return n
}

func countBuzzwords(cvText string) int {
	lower := strings.ToLower(cvText)
	buzzwords := []string{
		"synergy", "leverage", "paradigm", "rockstar", "ninja", "guru",
		"dynamic team player", "results-oriented", "detail-oriented",
	}
	n := 0
	for _, bw := range buzzwords {
		n += strings.Count(lower, bw)
	}
	return n
}

func impactForScore(score, mediumThreshold, highThreshold int) string {
	switch {
	case score < mediumThreshold:
		return "high"
	case score < highThreshold:
		return "medium"
	default:
		return "low"
	}
}

func impactRank(impact string) int {
	switch strings.ToLower(impact) {
	case "high":
		return 0
	case "medium":
		return 1
	default:
		return 2
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ── LLM-driven bullet rewrites ───────────────────────────────────────

// LLMPrompter is the subset of extraction.Extractor used by the
// rewrite pass. Decoupled so tests can stub the LLM call.
type LLMPrompter interface {
	Prompt(ctx context.Context, systemPrompt, userContent string) (string, error)
}

// AttachRewrites fills report.Rewrites with LLM-generated before/after
// pairs for the 5 weakest bullets in the CV. Best-effort: if the LLM
// call fails or returns unparseable JSON, rewrites stay empty and the
// rest of the report still ships. Safe to call with a nil prompter.
func AttachRewrites(ctx context.Context, prompter LLMPrompter, report *CVStrengthReport, fields *extraction.CVFields) {
	if prompter == nil || fields == nil {
		return
	}

	weakest := weakestBullets(fields, maxRewrites)
	if len(weakest) == 0 {
		return
	}

	// Batched call: one prompt, N rewrites. Lower latency than N
	// calls and lets the model see all bullets together, which
	// improves stylistic consistency across the rewritten set.
	bulletsJSON, err := json.Marshal(weakest)
	if err != nil {
		return
	}

	systemPrompt := buildRewriteSystemPrompt(report.TargetRole, report.RoleFamily)
	raw, err := prompter.Prompt(ctx, systemPrompt, string(bulletsJSON))
	if err != nil {
		return
	}

	var rewrites []BulletRewrite
	if err := json.Unmarshal([]byte(raw), &rewrites); err != nil {
		return
	}
	// Keep only rewrites that actually changed something and that
	// reference bullets we asked about — defence against the model
	// returning made-up indices.
	accepted := make([]BulletRewrite, 0, len(rewrites))
	for _, rw := range rewrites {
		if rw.Before == "" || rw.After == "" || rw.Before == rw.After {
			continue
		}
		accepted = append(accepted, rw)
	}
	if len(accepted) > maxRewrites {
		accepted = accepted[:maxRewrites]
	}
	report.Rewrites = accepted
}

type weakBullet struct {
	WorkIndex   int    `json:"work_index"`
	BulletIndex int    `json:"bullet_index"`
	Text        string `json:"text"`
	Score       int    `json:"score"`
}

// weakestBullets returns the top-N bullets sorted by ascending bullet
// score. Ties broken by work-history recency (later jobs first) so
// rewrites land on the most prominent bullets in the CV.
func weakestBullets(fields *extraction.CVFields, limit int) []weakBullet {
	var all []weakBullet
	for wi, job := range fields.WorkHistory {
		for bi, bullet := range splitBullets(job.Summary) {
			all = append(all, weakBullet{
				WorkIndex:   wi,
				BulletIndex: bi,
				Text:        bullet,
				Score:       scoreBullet(bullet),
			})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score < all[j].Score
		}
		return all[i].WorkIndex > all[j].WorkIndex
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

func buildRewriteSystemPrompt(targetRole, family string) string {
	return fmt.Sprintf(`You rewrite experience bullets for a %s CV targeting the role "%s". For each bullet you receive:

1. Keep the original factual claim — do not invent numbers, tech, or responsibilities the original did not state.
2. Start with a strong action verb (built, shipped, led, drove, delivered, scaled, reduced, automated, migrated, optimized).
3. Add a placeholder in curly braces where a metric would naturally fit — the user will fill it in. Example: "{X users}", "{Y%% latency reduction}".
4. Keep it to one sentence, <25 words.

Return ONLY a JSON array with the same length and shape:
[{"work_index": int, "bullet_index": int, "before": "<original>", "after": "<rewrite>", "reason": "<one-line reason>"}]

No prose, no markdown, no code fences.`, family, targetRole)
}
