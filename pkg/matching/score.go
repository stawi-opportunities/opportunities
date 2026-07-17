// Package matching contains the deterministic scoring function called
// by every matching path (fan-out, gap-filler, candidate-side rematch).
// Keep this file pure: no DB, no I/O, no time.Now() (caller passes now
// in so tests can pin it).
package matching

import (
	"math"
	"strings"
	"time"
)

// Weights configures the scoring function. Loaded from env at startup;
// changing weights between Score() calls is supported.
type Weights struct {
	Cosine float64 // w1
	Skills float64 // w2
	Geo    float64 // w3
	Salary float64 // w4
	Stale  float64 // p1 (subtracted)
}

// DefaultWeights are the starting weights. Tuned in env, not in code.
func DefaultWeights() Weights {
	return Weights{
		Cosine: 0.60,
		Skills: 0.15,
		Geo:    0.15,
		Salary: 0.10,
		Stale:  0.10,
	}
}

// CosineFromPGDistance converts pgvector cosine distance (typically 0–2)
// to a similarity in [0,1]. Shared by FanOut, GapFill, and legacy KNN so
// all paths use the same cosine term.
func CosineFromPGDistance(distance float64) float64 {
	c := 1.0 - distance/2.0
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return c
}

// BlendFromCosine applies DefaultWeights (or w) to a cosine similarity
// with neutral skills/geo/salary (1.0) and zero stale penalty — used when
// the legacy KNN path only has a cosine/similarity score so totals land
// on the same scale as FanOut/GapFill.
func BlendFromCosine(cosine float64, w Weights) float64 {
	if w == (Weights{}) {
		w = DefaultWeights()
	}
	if cosine < 0 {
		cosine = 0
	}
	if cosine > 1 {
		cosine = 1
	}
	// Neutral non-cosine terms = 1.0 (same as Score when one side missing).
	total := w.Cosine*cosine + w.Skills*1.0 + w.Geo*1.0 + w.Salary*1.0
	if total < 0 {
		return 0
	}
	return total
}

// CandidateSignal is the per-candidate input to scoring. Comes from
// candidate_match_indexes.
type CandidateSignal struct {
	Embedding      []float32
	Skills         []string
	Countries      []string // includes "remote" sentinel if remote-OK
	SalaryFloorUSD *int
}

// OpportunitySignal is the per-opportunity input to scoring.
type OpportunitySignal struct {
	Embedding    []float32
	Skills       []string
	Country      string // ISO 3166-1 alpha-2, or empty for unknown
	SalaryMaxUSD *int
	FirstSeenAt  time.Time
}

// Result is the score breakdown. Stored on candidate_match_events so
// operators can answer "why was this matched."
type Result struct {
	Cosine        float64
	SkillsOverlap float64
	GeoMatch      float64
	SalaryFit     float64
	StalePenalty  float64
	Total         float64
}

// Score computes a deterministic match score. now is provided by the
// caller so tests can pin time.
func Score(cand CandidateSignal, opp OpportunitySignal, w Weights, now time.Time) Result {
	r := Result{
		Cosine:        cosineSimilarity(cand.Embedding, opp.Embedding),
		SkillsOverlap: skillsOverlap(cand.Skills, opp.Skills),
		GeoMatch:      geoMatch(cand.Countries, opp.Country),
		SalaryFit:     salaryFit(cand.SalaryFloorUSD, opp.SalaryMaxUSD),
		StalePenalty:  stalePenalty(opp.FirstSeenAt, now),
	}
	r.Total = w.Cosine*r.Cosine +
		w.Skills*r.SkillsOverlap +
		w.Geo*r.GeoMatch +
		w.Salary*r.SalaryFit -
		w.Stale*r.StalePenalty
	if r.Total < 0 {
		r.Total = 0
	}
	return r
}

// cosineSimilarity returns 0 when either side is empty or dimensions
// mismatch. We never panic on bad embeddings — a 0 here causes the
// candidate to fall below threshold which is the correct behavior.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// skillsOverlap = |cand ∩ opp| / |cand|. Case-insensitive on the skill
// label. Missing data is neutral (1.0) so Path A/C (often without skill
// bags) stay on the same scale as BlendFromCosine.
func skillsOverlap(cand, opp []string) float64 {
	if len(cand) == 0 || len(opp) == 0 {
		return 1.0
	}
	want := make(map[string]struct{}, len(opp))
	for _, s := range opp {
		want[strings.ToLower(s)] = struct{}{}
	}
	var hits float64
	for _, s := range cand {
		if _, ok := want[strings.ToLower(s)]; ok {
			hits++
		}
	}
	return hits / float64(len(cand))
}

// geoMatch is 1 when the candidate accepts this country or "remote".
// Missing data is neutral (1.0); only a hard country mismatch scores 0.
func geoMatch(candCountries []string, oppCountry string) float64 {
	if len(candCountries) == 0 || oppCountry == "" {
		return 1.0
	}
	for _, c := range candCountries {
		if strings.EqualFold(c, oppCountry) || strings.EqualFold(c, "remote") {
			return 1.0
		}
	}
	return 0.0
}

// salaryFit is 1 when the opportunity meets or exceeds the candidate's
// floor. Missing data is neutral (1.0); only a known miss scores 0.
func salaryFit(candFloor, oppMax *int) float64 {
	if candFloor == nil || oppMax == nil {
		return 1.0
	}
	if *oppMax >= *candFloor {
		return 1.0
	}
	return 0.0
}

// stalePenalty is 0 for the first 7 days, then ramps linearly to 1 at
// day 60. The ramp subtracts the 7-day grace period so the curve is
// continuous at the day-7 boundary (no jump).
// Unspecified firstSeen (zero time) returns 0. Future timestamps (clock skew) return 0.
func stalePenalty(firstSeen, now time.Time) float64 {
	if firstSeen.IsZero() {
		return 0.0
	}
	age := now.Sub(firstSeen)
	if age <= 7*24*time.Hour {
		return 0.0
	}
	if age >= 60*24*time.Hour {
		return 1.0
	}
	span := float64((60 - 7) * 24 * 60 * 60)
	excess := age.Seconds() - float64(7*24*60*60)
	return excess / span
}
