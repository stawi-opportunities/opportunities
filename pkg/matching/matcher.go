package matching

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/repository"
)

// Matcher orchestrates the candidate↔job matching pipeline.
//
// Stage 1: hard filter via Postgres (region, salary, seniority, job_type).
// Stage 2: per-pair composite scoring (skills overlap, embedding sim,
//
//	quality, salary fit, recency, seniority fit).
//
// Stage 3: (optional) cross-encoder rerank of the top-K candidates from
//
//	stage 2 using BGE-reranker-v2-m3 via TEI. Scores are cached on
//	(cv_text ⊕ job_id ⊕ model_version) so weekly re-runs don't pay
//	twice for unchanged pairs.
//
// Stage 3 is feature-flagged per matcher; callers enable it via the
// WithReranker option. When disabled, MatchScore == composite score and
// RerankScore stays nil — exactly the behaviour before reranking landed.
type Matcher struct {
	jobRepo       *repository.JobRepository
	matchRepo     *repository.MatchRepository
	candidateRepo *repository.CandidateRepository

	// Optional reranker — nil-safe. Only populated when WithReranker was
	// called with a non-empty extractor.RerankerVersion().
	extractor     *extraction.Extractor
	rerankCache   *repository.RerankCacheRepository
	rerankTopK    int
	rerankEnabled bool
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
		rerankTopK:    100,
	}
}

// WithReranker turns on stage 3. topK caps how many pairs from stage 2
// are sent to the cross-encoder; 100 is the default when topK <= 0.
// Passing an extractor with no RerankerVersion() is a safe no-op.
func (m *Matcher) WithReranker(extractor *extraction.Extractor, cache *repository.RerankCacheRepository, topK int) *Matcher {
	if extractor == nil || extractor.RerankerVersion() == "" {
		return m
	}
	if topK <= 0 {
		topK = 100
	}
	m.extractor = extractor
	m.rerankCache = cache
	m.rerankTopK = topK
	m.rerankEnabled = true
	return m
}

const minMatchScore = 0.3

// scoredPair links a pool entry (post-stage-2 match) back to its source
// job for the rerank stage, which needs the job text.
type scoredPair struct {
	job          *domain.CanonicalJob
	match        *domain.CandidateMatch
	compositeVal float64
}

// MatchCandidateToJobs runs the matching pipeline for a single candidate
// and upserts the resulting rows into candidate_matches.
func (m *Matcher) MatchCandidateToJobs(ctx context.Context, candidate *domain.CandidateProfile) (int, error) {
	jobs, err := m.jobRepo.FilterForCandidate(ctx, candidate, 5000)
	if err != nil {
		return 0, err
	}
	candidateEmb := parseEmbedding(candidate.Embedding)
	candidateSkills := firstNonEmpty(candidate.Skills, candidate.StrongSkills)

	// Stage 2 — composite scoring.
	var pool []scoredPair
	for _, job := range jobs {
		jobEmb := parseEmbedding(job.Embedding)
		embSim := cosineSimilarity(candidateEmb, jobEmb)
		jobSkills := firstNonEmpty(job.RequiredSkills, job.Skills)

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
		retrievalScore := float32(score)
		pool = append(pool, scoredPair{
			job:          job,
			compositeVal: score,
			match: &domain.CandidateMatch{
				CandidateID:         candidate.ID,
				CanonicalJobID:      job.ID,
				MatchScore:          float32(score),
				SkillsOverlap:       float32(skillsScore),
				EmbeddingSimilarity: float32(embSim),
				RetrievalScore:      &retrievalScore,
			},
		})
	}

	// Stage 3 — cross-encoder rerank on top-K by composite. This only
	// reorders the pool; it doesn't drop matches that didn't make the
	// top-K (those keep their composite score).
	if m.rerankEnabled {
		sort.Slice(pool, func(i, j int) bool { return pool[i].compositeVal > pool[j].compositeVal })
		topN := m.rerankTopK
		if topN > len(pool) {
			topN = len(pool)
		}
		if topN > 0 {
			m.rerankTop(ctx, candidate, pool[:topN])
		}
	}

	matches := make([]*domain.CandidateMatch, 0, len(pool))
	for _, s := range pool {
		matches = append(matches, s.match)
	}
	if err := m.matchRepo.UpsertBatch(ctx, matches); err != nil {
		return 0, err
	}
	return len(matches), nil
}

// rerankTop runs the cross-encoder against the given pool entries,
// writes the scores onto each .match, and cache-memoises each result.
// Any error is logged and the pool is left with bi-encoder scores only —
// the pipeline never breaks on a reranker failure.
func (m *Matcher) rerankTop(ctx context.Context, candidate *domain.CandidateProfile, pool []scoredPair) {
	version := m.extractor.RerankerVersion()
	query := buildCandidateQuery(candidate)

	// 1. Check cache in one pass; collect misses to batch.
	texts := make([]string, 0, len(pool))
	idxOfMiss := make([]int, 0, len(pool))
	keys := make([]string, len(pool))
	for i := range pool {
		key := repository.RerankCacheKey(query, pool[i].job.ID, version)
		keys[i] = key
		if m.rerankCache != nil {
			if s, hit, err := m.rerankCache.Get(ctx, key); err == nil && hit {
				applyRerank(pool[i].match, s, version, i)
				continue
			}
		}
		texts = append(texts, buildJobText(pool[i].job))
		idxOfMiss = append(idxOfMiss, i)
	}

	// 2. One cross-encoder call for the misses.
	if len(texts) > 0 {
		scores, err := m.extractor.Rerank(ctx, query, texts)
		if err != nil {
			log.Printf("matching: rerank call failed (falling back to retrieval order): %v", err)
			// Populate RerankScore from composite so downstream ordering
			// stays deterministic.
			for _, i := range idxOfMiss {
				applyRerank(pool[i].match, float32(pool[i].compositeVal), version, i)
			}
			return
		}
		for k, i := range idxOfMiss {
			if k >= len(scores) {
				break
			}
			applyRerank(pool[i].match, scores[k], version, i)
			if m.rerankCache != nil {
				if err := m.rerankCache.Put(ctx, keys[i], scores[k], version); err != nil {
					log.Printf("matching: rerank cache put failed (non-fatal): %v", err)
				}
			}
		}
	}

	// 3. Re-sort pool and promote reranker score into MatchScore so the
	//    DB-level ordering reflects stage 3 output.
	sort.Slice(pool, func(i, j int) bool {
		var a, b float32
		if pool[i].match.RerankScore != nil {
			a = *pool[i].match.RerankScore
		}
		if pool[j].match.RerankScore != nil {
			b = *pool[j].match.RerankScore
		}
		return a > b
	})
	for i := range pool {
		if pool[i].match.RerankScore != nil {
			pool[i].match.MatchScore = *pool[i].match.RerankScore
		}
		rr := i
		pool[i].match.RetrievalRank = &rr
	}
}

func applyRerank(match *domain.CandidateMatch, score float32, version string, _ int) {
	match.RerankScore = &score
	match.RerankerVersion = version
	now := time.Now()
	match.RerankedAt = &now
}

// MatchJobToCandidates runs the matching pipeline for a single job.
// Reranker is deliberately not applied here in v1 — the interesting use
// case is weekly candidate-centric delivery, not job-centric fanout.
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
		candidateSkills := firstNonEmpty(candidate.Skills, candidate.StrongSkills)
		jobSkills := firstNonEmpty(job.RequiredSkills, job.Skills)

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
		retrieval := float32(score)
		matches = append(matches, &domain.CandidateMatch{
			CandidateID:         candidate.ID,
			CanonicalJobID:      job.ID,
			MatchScore:          float32(score),
			SkillsOverlap:       float32(skillsScore),
			EmbeddingSimilarity: float32(embSim),
			RetrievalScore:      &retrieval,
		})
	}

	if err := m.matchRepo.UpsertBatch(ctx, matches); err != nil {
		return 0, err
	}
	return len(matches), nil
}

// buildCandidateQuery distils a candidate profile down to the text the
// cross-encoder sees as the "query" side of the pair. Kept short — cross
// encoders truncate long inputs internally anyway and longer queries
// don't help.
func buildCandidateQuery(c *domain.CandidateProfile) string {
	parts := []string{}
	if c.TargetJobTitle != "" {
		parts = append(parts, c.TargetJobTitle)
	} else if c.CurrentTitle != "" {
		parts = append(parts, c.CurrentTitle)
	}
	if c.Seniority != "" {
		parts = append(parts, c.Seniority)
	}
	if c.StrongSkills != "" {
		parts = append(parts, c.StrongSkills)
	} else if c.Skills != "" {
		parts = append(parts, c.Skills)
	}
	if c.Bio != "" {
		parts = append(parts, truncate(c.Bio, 400))
	}
	return strings.Join(parts, " · ")
}

// buildJobText is the "document" side of the cross-encoder pair.
func buildJobText(j *domain.CanonicalJob) string {
	parts := []string{j.Title}
	if j.Company != "" {
		parts = append(parts, j.Company)
	}
	if j.RequiredSkills != "" {
		parts = append(parts, j.RequiredSkills)
	} else if j.Skills != "" {
		parts = append(parts, j.Skills)
	}
	if j.Description != "" {
		parts = append(parts, truncate(j.Description, 600))
	}
	return strings.Join(parts, " · ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 3 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// parseEmbedding deserialises a JSON-encoded []float32 embedding vector.
// An empty or malformed string returns nil (cosineSimilarity treats nil
// as a zero-score pair).
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
