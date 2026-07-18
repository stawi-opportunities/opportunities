package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

func TestHeuristicJobFit_SharedKeywords(t *testing.T) {
	t.Parallel()
	profile := "Senior backend engineer with golang kubernetes postgres distributed systems experience building APIs"
	job := "We need a backend engineer skilled in golang, kubernetes, and postgres for distributed systems"
	fit := heuristicJobFit(profile, job, "Backend Engineer")
	require.GreaterOrEqual(t, fit.Score, 40)
	require.Equal(t, "keywords", fit.Method)
	require.NotEmpty(t, fit.Signals)
}

func TestHeuristicJobFit_EmptyProfile(t *testing.T) {
	t.Parallel()
	fit := heuristicJobFit("", "looking for a senior product manager with roadmap experience", "PM")
	require.Equal(t, 0, fit.Score)
	require.Equal(t, "weak", fit.Label)
}

func TestCosineSim_Identical(t *testing.T) {
	t.Parallel()
	v := []float32{1, 0, 0}
	require.InDelta(t, 1.0, cosineSim(v, v), 1e-6)
}

func TestCosineSim_Orthogonal(t *testing.T) {
	t.Parallel()
	a := []float32{1, 0}
	b := []float32{0, 1}
	require.InDelta(t, 0.0, cosineSim(a, b), 1e-6)
}

func TestCosineSim_LengthMismatch(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0.0, cosineSim([]float32{1}, []float32{1, 0}))
}

type stubEmbedder struct {
	vec []float32
	err error
}

func (s stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return s.vec, s.err
}

func TestJobFitHandler_KeywordsOnlyWithoutEmbedder(t *testing.T) {
	t.Parallel()
	// No DB / embedder / index → pure keyword path.
	h := httpmw.NewCandidateAuth(nil)(JobFitHandler(ToolsDeps{}))
	body := `{"job_text":"We are hiring a backend engineer with golang kubernetes and postgres experience for our platform team building APIs","title":"Backend Engineer"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/tools/job-fit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Candidate-ID", "cand_tools")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var out JobFitResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "keywords", out.Method)
	require.NotNil(t, out.KeywordScore)
	require.Nil(t, out.VectorScore)
	// Empty profile → weak score but still returns.
	require.Equal(t, "weak", out.Label)
}

func TestJobFitHandler_RejectsShortJobText(t *testing.T) {
	t.Parallel()
	h := httpmw.NewCandidateAuth(nil)(JobFitHandler(ToolsDeps{}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/tools/job-fit", strings.NewReader(`{"job_text":"too short"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Candidate-ID", "cand_tools")
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestJobFitHandler_LiveVectorBlend(t *testing.T) {
	t.Parallel()
	// Without Index/DB, candidateEmbedding falls back to live profile embed —
	// but loadProfileText returns "" with nil DB, so vector path is skipped
	// unless we only test cosine via vectorJobFit with a custom embedder that
	// still needs profile. Here we assert that providing an embedder alone
	// without profile still degrades to keywords (no crash).
	h := httpmw.NewCandidateAuth(nil)(JobFitHandler(ToolsDeps{
		Embedder: stubEmbedder{vec: []float32{0.1, 0.2, 0.3}},
	}))
	body := `{"job_text":"Senior product designer needed for mobile design systems and accessibility research portfolio","title":"Product Designer"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/me/tools/job-fit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Candidate-ID", "cand_tools")
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out JobFitResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "keywords", out.Method)
}

func TestFitLabel(t *testing.T) {
	t.Parallel()
	require.Equal(t, "strong", fitLabel(65))
	require.Equal(t, "moderate", fitLabel(40))
	require.Equal(t, "weak", fitLabel(39))
}

