package cv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strings"
	"time"

	"stawi.jobs/pkg/extraction"
)

// Weights sum to 100. Changing these is a user-visible score
// recalculation — bump the report's CVVersion scheme in lockstep so
// cached reports don't straddle different weight sets.
const (
	weightATS      = 20
	weightKeywords = 20
	weightImpact   = 25
	weightRoleFit  = 20
	weightClarity  = 15
)

// Embedder is the narrow subset of extraction.Extractor the scorer
// needs. Defined as an interface so tests can stub without touching
// the full Groq/Embedding wiring.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Scorer computes a CVStrengthReport. Deterministic components run in
// pure functions; the role-fit component goes through the embedder.
// A nil embedder downgrades role-fit to a neutral 60 rather than
// failing outright.
type Scorer struct {
	embedder Embedder
}

// NewScorer constructs a scorer. Pass the extraction package's
// Extractor (which already wraps both chat + embed clients).
func NewScorer(embedder Embedder) *Scorer {
	return &Scorer{embedder: embedder}
}

// Score runs the full pipeline and returns an unfilled report:
// scores + priority fixes are set here, but the LLM-driven bullet
// rewrites are populated separately by fixes.go so callers can opt
// out of the extra LLM round-trip when they only need the score.
func (s *Scorer) Score(ctx context.Context, cvText string, fields *extraction.CVFields, targetRole string) *CVStrengthReport {
	role := strings.TrimSpace(targetRole)
	if role == "" && fields != nil {
		role = fields.CurrentTitle
	}
	family := DetectRoleFamily(role)

	report := &CVStrengthReport{
		TargetRole: role,
		RoleFamily: string(family),
		GeneratedAt: time.Now().UTC(),
		CVVersion:  versionHash(cvText),
	}

	report.Components.ATS = scoreATS(cvText, fields)
	report.Components.Keywords = scoreKeywords(cvText, family)
	report.Components.Impact = scoreImpact(fields)
	report.Components.Clarity = scoreClarity(cvText)
	report.Components.RoleFit = s.scoreRoleFit(ctx, cvText, family)

	report.OverallScore = weightedOverall(report.Components)
	report.PriorityFixes = detectPriorityFixes(cvText, fields, family, report.Components)

	return report
}

// weightedOverall combines components with the documented weights.
// The output is integer 0-100 — half-points are rounded away so the
// UI never has to display fractions.
func weightedOverall(c ScoreComponents) int {
	raw := float64(c.ATS*weightATS +
		c.Keywords*weightKeywords +
		c.Impact*weightImpact +
		c.RoleFit*weightRoleFit +
		c.Clarity*weightClarity)
	return int(math.Round(raw / 100.0))
}

// versionHash is a short content hash used to invalidate cached
// reports when the underlying CV text changes. First 16 hex chars are
// more than enough for uniqueness at our scale.
func versionHash(cvText string) string {
	sum := sha256.Sum256([]byte(cvText))
	return hex.EncodeToString(sum[:8])
}

// VersionHashText exposes the hash for staleness detection outside
// this package (e.g. the candidates HTTP handlers comparing a cached
// report version against the live CV text).
func VersionHashText(cvText string) string { return versionHash(cvText) }

// ── ATS (0-100) ──────────────────────────────────────────────────────
// Penalises everything an applicant tracking system can't parse.
// Start at 100 and deduct for missing structural signals; floor at 0.
func scoreATS(cvText string, fields *extraction.CVFields) int {
	score := 100
	lower := strings.ToLower(cvText)

	// Length sanity. Very short = under-developed; very long = walls
	// of text. Both hurt ATS parsing.
	runeLen := len([]rune(cvText))
	switch {
	case runeLen < 500:
		score -= 30
	case runeLen > 15000:
		score -= 15
	}

	// Standard section headers. Each missing one costs 10 points so a
	// CV without Experience, Skills, and Education can still score in
	// the 60s if everything else is clean.
	requiredSections := []string{"experience", "skills", "education"}
	for _, section := range requiredSections {
		if !strings.Contains(lower, section) {
			score -= 10
		}
	}

	// Work history entries are the strongest structural signal.
	if fields != nil && len(fields.WorkHistory) == 0 {
		score -= 25
	}

	// A Summary/About section up top is how recruiters scan in 10s.
	if !containsAny(lower, "summary", "professional summary", "about", "profile") {
		score -= 10
	}

	// Tables and column layouts are ATS poison; heuristic detection
	// flags "|" or "•" clustering, which is the most common form.
	if strings.Count(cvText, "|") > 20 {
		score -= 10
	}

	return clamp(score, 0, 100)
}

// ── Keywords (0-100) ─────────────────────────────────────────────────
// Matches the CV text against the expected-keyword list for the
// detected role family (plus synonyms). Score = hit rate × 100.
func scoreKeywords(cvText string, family RoleFamily) int {
	expected := expectedKeywords[family]
	if len(expected) == 0 {
		return 60 // unknown family — don't penalise aggressively
	}
	lower := strings.ToLower(cvText)

	matched := 0
	for _, kw := range expected {
		if keywordPresent(lower, kw) {
			matched++
		}
	}
	return clamp(int(math.Round(float64(matched)/float64(len(expected))*100)), 0, 100)
}

// keywordPresent does the canonical + synonym lookup using simple
// substring checks. Word-boundary regex would be more precise but is
// 10× slower on long CVs and the false-positive rate of substring
// matches is negligible at this scale.
func keywordPresent(lowerText, keyword string) bool {
	if strings.Contains(lowerText, keyword) {
		return true
	}
	for _, syn := range keywordSynonyms[keyword] {
		if strings.Contains(lowerText, syn) {
			return true
		}
	}
	return false
}

// ── Impact (0-100) ───────────────────────────────────────────────────
// Scans each bullet / summary sentence for signals of quantified
// outcome (numbers, units, strong verbs). Each bullet's score is
// averaged; the overall component is this average.
func scoreImpact(fields *extraction.CVFields) int {
	if fields == nil || len(fields.WorkHistory) == 0 {
		return 30 // no work history → inherent low cap
	}

	var bulletScores []int
	for _, job := range fields.WorkHistory {
		for _, bullet := range splitBullets(job.Summary) {
			bulletScores = append(bulletScores, scoreBullet(bullet))
		}
	}
	if len(bulletScores) == 0 {
		return 30
	}

	sum := 0
	for _, s := range bulletScores {
		sum += s
	}
	return clamp(sum/len(bulletScores), 0, 100)
}

// scoreBullet rates a single sentence / bullet point. Returns 0-100.
func scoreBullet(bullet string) int {
	lower := strings.ToLower(strings.TrimSpace(bullet))
	if lower == "" {
		return 0
	}

	score := 50 // neutral floor — a bullet that neither helps nor hurts

	// Quantified impact: numbers + a unit. "Built APIs" is weak;
	// "Built APIs handling 2M requests/min" is strong.
	if numberPattern.MatchString(bullet) {
		score += 15
	}
	if hasImpactUnit(lower) {
		score += 10
	}

	// Strong action verb at the start. Recruiters expect the bullet
	// to start with a verb, not "Responsible for...".
	if startsWithStrongVerb(lower) {
		score += 15
	}
	if startsWithWeakVerb(lower) {
		score -= 25
	}

	// Outcome language — bullets mentioning the effect of the work
	// score higher than those that only describe the work.
	if containsAny(lower, "resulting in", "leading to", "which improved", "which reduced", "which increased", "achieving", "enabled", "unlocked") {
		score += 10
	}

	return clamp(score, 0, 100)
}

var numberPattern = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?[%kKmMbB]?\b`)

func hasImpactUnit(lower string) bool {
	return containsAny(lower,
		"%", " ms", "ms ", "requests/", "req/s", "rps", "qps",
		"$", "usd", "eur", "gbp",
		"users", "customers", "clients",
		"engineers", "members", "reports", "tb", "gb",
	)
}

var strongVerbs = []string{
	"built", "designed", "architected", "shipped", "launched", "led",
	"drove", "owned", "delivered", "scaled", "reduced", "increased",
	"automated", "migrated", "optimized", "optimised", "rewrote",
	"implemented", "created", "developed", "established", "grew",
	"mentored", "hired", "coached", "negotiated", "closed",
}

var weakVerbs = []string{
	"worked on", "worked with", "helped", "assisted", "responsible for",
	"participated in", "involved in", "contributed to", "tasked with",
}

func startsWithStrongVerb(lower string) bool {
	for _, v := range strongVerbs {
		if strings.HasPrefix(lower, v+" ") || strings.HasPrefix(lower, v+",") {
			return true
		}
	}
	return false
}

func startsWithWeakVerb(lower string) bool {
	for _, v := range weakVerbs {
		if strings.HasPrefix(lower, v) {
			return true
		}
	}
	return false
}

// splitBullets breaks a work-history summary into individual bullets.
// Handles common formats: newline-separated, bullet-character prefixed,
// or period-terminated sentences.
func splitBullets(summary string) []string {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	// Prefer explicit newlines — LLM extraction usually preserves
	// them from the source CV.
	lines := strings.Split(summary, "\n")
	var out []string
	for _, line := range lines {
		// Strip common bullet prefixes.
		line = strings.TrimLeft(line, "-•*· ")
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) > 1 {
		return out
	}
	// Fall back to sentence split on periods if the whole summary is
	// one line.
	for _, sent := range strings.Split(summary, ". ") {
		s := strings.TrimSpace(sent)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ── Clarity (0-100) ──────────────────────────────────────────────────
// Measures prose hygiene: sentence length, passive voice, buzzword
// density. Deductive: start at 100, subtract for each red flag.
func scoreClarity(cvText string) int {
	score := 100
	lower := strings.ToLower(cvText)

	// Buzzword soup. Small list on purpose — over-counting penalises
	// CVs that legitimately mention strategy or synergy once.
	buzzwords := []string{
		"synergy", "leverage", "paradigm", "value-add",
		"go-getter", "rockstar", "ninja", "guru",
		"dynamic team player", "results-oriented",
		"detail-oriented",
	}
	for _, bw := range buzzwords {
		if strings.Contains(lower, bw) {
			score -= 5
		}
	}

	// Passive-voice heuristic — "was/were + past participle". Not
	// perfect but good enough to nudge CVs towards active voice.
	passiveHits := passivePattern.FindAllString(lower, -1)
	if len(passiveHits) > 3 {
		score -= 10
	}
	if len(passiveHits) > 8 {
		score -= 10
	}

	// Average sentence length. Very long sentences (>30 words) are
	// harder to scan; very short (<5) look like fragments.
	avgLen := averageSentenceLength(cvText)
	if avgLen > 30 || (avgLen > 0 && avgLen < 5) {
		score -= 10
	}

	// "Responsible for" is the single most corrosive recruiter-cliche
	// phrase. Called out even though it's also caught in weakVerbs.
	if strings.Count(lower, "responsible for") >= 2 {
		score -= 10
	}

	return clamp(score, 0, 100)
}

// Deliberately conservative — matches "was/were/been/being" + adjacent
// past participle ending in "ed/en". False positives on adjectives are
// rare and don't noticeably skew the score.
var passivePattern = regexp.MustCompile(`\b(was|were|been|being|is|are)\s+\w+(ed|en)\b`)

func averageSentenceLength(cvText string) float64 {
	sentences := regexp.MustCompile(`[.!?]+`).Split(cvText, -1)
	var words, nonEmpty int
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		nonEmpty++
		words += len(strings.Fields(s))
	}
	if nonEmpty == 0 {
		return 0
	}
	return float64(words) / float64(nonEmpty)
}

// ── RoleFit (0-100) ──────────────────────────────────────────────────
// Cosine similarity between the CV text's embedding and a canonical
// reference paragraph for the detected role family. Degrades to 60
// (neutral) if embedding is unavailable so the rest of the report
// still ships.
func (s *Scorer) scoreRoleFit(ctx context.Context, cvText string, family RoleFamily) int {
	if s.embedder == nil {
		return 60
	}
	reference := roleReferenceText[family]
	if reference == "" {
		reference = roleReferenceText[FamilyGeneral]
	}

	cvVec, err := s.embedder.Embed(ctx, cvText)
	if err != nil || len(cvVec) == 0 {
		return 60
	}
	refVec, err := s.embedder.Embed(ctx, reference)
	if err != nil || len(refVec) == 0 {
		return 60
	}

	sim := cosineSimilarity(cvVec, refVec)
	// Cosine runs -1..1; real-world CVs against reference paragraphs
	// hover 0.3-0.8. Rescale so 0.3→0 and 0.8→100; clamp edges.
	scaled := (sim - 0.3) / 0.5 * 100
	return clamp(int(math.Round(scaled)), 0, 100)
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
