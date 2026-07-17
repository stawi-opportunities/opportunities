package matching_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestScore_CosineOnly(t *testing.T) {
	// Two identical normalized vectors → cosine = 1; missing skills/geo/salary neutral.
	cand := matching.CandidateSignal{Embedding: []float32{1, 0, 0}}
	opp := matching.OpportunitySignal{Embedding: []float32{1, 0, 0}}
	w := matching.DefaultWeights()
	got := matching.Score(cand, opp, w, time.Now())
	want := w.Cosine + w.Skills + w.Geo + w.Salary
	require.InDelta(t, want, got.Total, 1e-6)
	require.InDelta(t, 1.0, got.Cosine, 1e-6)
}

func TestScore_OrthogonalVectors(t *testing.T) {
	cand := matching.CandidateSignal{Embedding: []float32{1, 0, 0}}
	opp := matching.OpportunitySignal{Embedding: []float32{0, 1, 0}}
	got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
	require.InDelta(t, 0.0, got.Cosine, 1e-6)
}

func TestScore_SkillsOverlap(t *testing.T) {
	cand := matching.CandidateSignal{
		Embedding: []float32{1, 0, 0},
		Skills:    []string{"go", "postgres", "kubernetes"},
	}
	opp := matching.OpportunitySignal{
		Embedding: []float32{1, 0, 0},
		Skills:    []string{"go", "postgres"},
	}
	w := matching.DefaultWeights()
	got := matching.Score(cand, opp, w, time.Now())
	// 2 of 3 candidate skills present → 0.666...
	require.InDelta(t, 2.0/3.0, got.SkillsOverlap, 1e-6)
	// Missing geo/salary are neutral (1.0), cosine=1.
	want := w.Cosine*1.0 + w.Skills*(2.0/3.0) + w.Geo*1.0 + w.Salary*1.0
	require.InDelta(t, want, got.Total, 1e-6)
}

func TestScore_GeoMatch(t *testing.T) {
	tests := []struct {
		name string
		cand []string
		opp  string
		want float64
	}{
		{"exact match", []string{"KE", "UG"}, "KE", 1.0},
		{"no overlap", []string{"KE"}, "ZA", 0.0},
		{"remote token matches anywhere", []string{"remote"}, "ZA", 1.0},
		{"no opp country is neutral 1", []string{"KE"}, "", 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cand := matching.CandidateSignal{
				Embedding: []float32{1, 0, 0}, Countries: tc.cand,
			}
			opp := matching.OpportunitySignal{
				Embedding: []float32{1, 0, 0}, Country: tc.opp,
			}
			got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
			require.Equal(t, tc.want, got.GeoMatch)
		})
	}
}

func TestScore_SalaryFit(t *testing.T) {
	floor := 50000
	tests := []struct {
		name       string
		candFloor  *int
		oppMax     *int
		wantFitMin float64
		wantFitMax float64
	}{
		{"opp max above floor — perfect", &floor, ptrInt(80000), 1.0, 1.0},
		{"opp max equals floor — perfect", &floor, ptrInt(50000), 1.0, 1.0},
		{"opp max below floor — 0", &floor, ptrInt(30000), 0.0, 0.0},
		{"no candidate floor — neutral 1", nil, ptrInt(30000), 1.0, 1.0},
		{"no opp max — neutral 1", &floor, nil, 1.0, 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cand := matching.CandidateSignal{
				Embedding: []float32{1, 0, 0}, SalaryFloorUSD: tc.candFloor,
			}
			opp := matching.OpportunitySignal{
				Embedding: []float32{1, 0, 0}, SalaryMaxUSD: tc.oppMax,
			}
			got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
			require.GreaterOrEqual(t, got.SalaryFit, tc.wantFitMin)
			require.LessOrEqual(t, got.SalaryFit, tc.wantFitMax)
		})
	}
}

func TestScore_StalePenalty(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cand := matching.CandidateSignal{Embedding: []float32{1, 0, 0}}
	tests := []struct {
		name      string
		firstSeen time.Time
		wantPen   float64
	}{
		{"fresh — 0 penalty", now.Add(-1 * time.Hour), 0.0},
		{"7 days old — 0 penalty", now.Add(-7 * 24 * time.Hour), 0.0},
		// Just past the grace period — penalty should be near 0 (continuity).
		{"7 days + 1 hour — near 0", now.Add(-(7*24 + 1) * time.Hour), 1.0 / (53 * 24)},
		// 30 days old: (30-7)/53 ≈ 0.434
		{"30 days old — partial penalty", now.Add(-30 * 24 * time.Hour), 23.0 / 53.0},
		{"60 days old — full penalty", now.Add(-60 * 24 * time.Hour), 1.0},
		{"future seen (clock skew) — 0", now.Add(1 * time.Hour), 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opp := matching.OpportunitySignal{
				Embedding:   []float32{1, 0, 0},
				FirstSeenAt: tc.firstSeen,
			}
			got := matching.Score(cand, opp, matching.DefaultWeights(), now)
			require.InDelta(t, tc.wantPen, got.StalePenalty, 0.01)
		})
	}
}

func TestScore_Bounded(t *testing.T) {
	// Total is bounded into [0, sum(positive weights)].
	cand := matching.CandidateSignal{
		Embedding: []float32{1, 0, 0},
		Skills:    []string{"go"},
		Countries: []string{"KE"},
	}
	opp := matching.OpportunitySignal{
		Embedding:   []float32{1, 0, 0},
		Skills:      []string{"go"},
		Country:     "KE",
		FirstSeenAt: time.Now(),
	}
	w := matching.DefaultWeights()
	got := matching.Score(cand, opp, w, time.Now())
	maxPossible := w.Cosine + w.Skills + w.Geo + w.Salary
	require.GreaterOrEqual(t, got.Total, 0.0)
	require.LessOrEqual(t, got.Total, maxPossible)
}

func TestScore_MissingEmbeddings(t *testing.T) {
	// Empty embedding on either side → cosine = 0, score still defined.
	cand := matching.CandidateSignal{Embedding: nil}
	opp := matching.OpportunitySignal{Embedding: []float32{1, 0, 0}}
	got := matching.Score(cand, opp, matching.DefaultWeights(), time.Now())
	require.InDelta(t, 0.0, got.Cosine, 1e-9)
	require.False(t, math.IsNaN(got.Total))
}

func ptrInt(v int) *int { return &v }
