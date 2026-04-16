package matching

import (
	"context"
	"encoding/json"
	"math"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
)

// Matcher orchestrates the three-stage candidate↔job matching pipeline.
type Matcher struct {
	jobRepo       *repository.JobRepository
	matchRepo     *repository.MatchRepository
	candidateRepo *repository.CandidateRepository
}

// NewMatcher constructs a Matcher with the required repository dependencies.
func NewMatcher(
	jobRepo *repository.JobRepository,
	matchRepo *repository.MatchRepository,
	candidateRepo *repository.CandidateRepository,
) *Matcher {
	return &Matcher{
		jobRepo:       jobRepo,
		matchRepo:     matchRepo,
		candidateRepo: candidateRepo,
	}
}

const minMatchScore = 0.3

// MatchCandidateToJobs runs the full matching pipeline for a single candidate:
//  1. Stage 1 hard filter — retrieve up to 5000 active jobs that pass the
//     candidate's remote/salary/country constraints.
//  2. Stage 2 embedding similarity — cosine similarity on JSON float32 vectors.
//  3. Stage 3 composite scoring — weighted combination of all sub-scores.
//
// Matches with a composite score >= 0.6 are upserted in batch.  The number of
// persisted matches is returned.
func (m *Matcher) MatchCandidateToJobs(ctx context.Context, candidate *domain.CandidateProfile) (int, error) {
	// Stage 1 — hard filter.
	jobs, err := m.jobRepo.FilterForCandidate(ctx, candidate, 5000)
	if err != nil {
		return 0, err
	}

	candidateEmb := parseEmbedding(candidate.Embedding)

	// Combine all candidate skills for matching
	candidateSkills := candidate.Skills
	if candidateSkills == "" {
		candidateSkills = candidate.StrongSkills
	}

	var matches []*domain.CandidateMatch
	for _, job := range jobs {
		jobEmb := parseEmbedding(job.Embedding)
		embSim := cosineSimilarity(candidateEmb, jobEmb)

		// Use RequiredSkills first, fall back to Skills
		jobSkills := job.RequiredSkills
		if jobSkills == "" {
			jobSkills = job.Skills
		}

		skillsScore := SkillsOverlap(candidateSkills, jobSkills)
		salaryScore := SalaryFit(
			float64(candidate.SalaryMin), float64(candidate.SalaryMax),
			job.SalaryMin, job.SalaryMax,
		)
		recencyScore := Recency(job.LastSeenAt)
		seniorityScore := SeniorityFit(candidate.Seniority, job.Seniority)

		hasEmbeddings := len(candidateEmb) > 0 && len(jobEmb) > 0
		hasSkills := candidateSkills != "" && jobSkills != ""

		score := ComputeMatchScore(skillsScore, embSim, job.QualityScore, salaryScore, recencyScore, seniorityScore, hasEmbeddings, hasSkills)
		if score < minMatchScore {
			continue
		}

		matches = append(matches, &domain.CandidateMatch{
			CandidateID:         candidate.ID,
			CanonicalJobID:      job.ID,
			MatchScore:          float32(score),
			SkillsOverlap:       float32(skillsScore),
			EmbeddingSimilarity: float32(embSim),
		})
	}

	if err := m.matchRepo.UpsertBatch(ctx, matches); err != nil {
		return 0, err
	}
	return len(matches), nil
}

// MatchJobToCandidates runs the full matching pipeline for a single job:
//  1. Retrieve up to 10 000 active candidates.
//  2. Compute composite scores for each.
//  3. Upsert matches with score >= 0.6.
//
// The number of persisted matches is returned.
func (m *Matcher) MatchJobToCandidates(ctx context.Context, job *domain.CanonicalJob) (int, error) {
	candidates, err := m.candidateRepo.ListActive(ctx, 10000)
	if err != nil {
		return 0, err
	}

	jobEmb := parseEmbedding(job.Embedding)

	var matches []*domain.CandidateMatch
	for _, candidate := range candidates {
		candidateEmb := parseEmbedding(candidate.Embedding)
		embSim := cosineSimilarity(candidateEmb, jobEmb)

		candidateSkills := candidate.Skills
		if candidateSkills == "" {
			candidateSkills = candidate.StrongSkills
		}
		jobSkills := job.RequiredSkills
		if jobSkills == "" {
			jobSkills = job.Skills
		}

		skillsScore := SkillsOverlap(candidateSkills, jobSkills)
		salaryScore := SalaryFit(
			float64(candidate.SalaryMin), float64(candidate.SalaryMax),
			job.SalaryMin, job.SalaryMax,
		)
		recencyScore := Recency(job.LastSeenAt)
		seniorityScore := SeniorityFit(candidate.Seniority, job.Seniority)

		hasEmbeddings := len(candidateEmb) > 0 && len(jobEmb) > 0
		hasSkills := candidateSkills != "" && jobSkills != ""

		score := ComputeMatchScore(skillsScore, embSim, job.QualityScore, salaryScore, recencyScore, seniorityScore, hasEmbeddings, hasSkills)
		if score < minMatchScore {
			continue
		}

		matches = append(matches, &domain.CandidateMatch{
			CandidateID:         candidate.ID,
			CanonicalJobID:      job.ID,
			MatchScore:          float32(score),
			SkillsOverlap:       float32(skillsScore),
			EmbeddingSimilarity: float32(embSim),
		})
	}

	if err := m.matchRepo.UpsertBatch(ctx, matches); err != nil {
		return 0, err
	}
	return len(matches), nil
}

// parseEmbedding deserialises a JSON-encoded []float32 embedding vector.
// An empty or malformed string returns nil (the cosine function handles nil
// gracefully by returning 0).
func parseEmbedding(raw string) []float32 {
	if raw == "" {
		return nil
	}
	var vec []float32
	if err := json.Unmarshal([]byte(raw), &vec); err != nil {
		return nil
	}
	return vec
}

// cosineSimilarity returns the cosine similarity of two float32 vectors.
// Returns 0 if either vector is nil, empty, or has zero magnitude.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dot += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
