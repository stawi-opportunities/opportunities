//go:build integration

package v1_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/me/v1"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupExtensionEnv(t *testing.T) (*http.ServeMux, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, db)

	mux := http.NewServeMux()
	// Header-only auth for integration tests (X-Candidate-ID). Production
	// main.go always passes NewCandidateAuth(authenticator) instead.
	v1.Mount(mux, &v1.Deps{
		DB:               db,
		Matches:          matching.NewStore(db),
		MatchEvents:      matching.NewEventLog(db),
		Rules:            matching.NewRulesStore(db),
		IndexStore:       matching.NewIndexStore(db),
		KNN:              matching.NewKNN(db),
		Reranker:         matching.NoopReranker{},
		Weights:          matching.DefaultWeights(),
		Debouncer:        matching.NewMemoryDebouncer(),
		IdempotencyStore: applications.NewIdempotencyStore(db, time.Hour),
	}, httpmw.NewCandidateAuth(nil))
	return mux, db, ctx
}

func doMe(t *testing.T, mux *http.ServeMux, method, path string, body any, cand, idemKey string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body == nil {
		rdr = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rdr)
	if cand != "" {
		r.Header.Set("X-Candidate-ID", cand)
	}
	if idemKey != "" {
		r.Header.Set("Idempotency-Key", idemKey)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestMeHandler(t *testing.T) {
	mux, _, _ := setupExtensionEnv(t)
	w := doMe(t, mux, "GET", "/api/me", nil, "u_me", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	require.Equal(t, "u_me", resp["candidate_id"])
	require.Equal(t, false, resp["autoapply_enabled"])
}

func TestMatchesListAndDetail(t *testing.T) {
	mux, db, ctx := setupExtensionEnv(t)
	// Seed two matches directly.
	for i, oid := range []string{"o1", "o2"} {
		_, err := db.ExecContext(ctx, `
INSERT INTO opportunities (canonical_id, slug, kind, title, apply_url, status, hidden)
VALUES ($1::varchar(20), $1::text, 'job', $1::text, $2, 'active', false)`, oid, "https://example.test/apply/"+oid)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
INSERT INTO candidate_matches (match_id, candidate_id, opportunity_id, status, score, last_event_id)
VALUES ($1, $2, $3, 'new', $4, '')
`, "m"+oid, "u_list", oid, 0.9-float64(i)*0.1)
		require.NoError(t, err)
	}

	w := doMe(t, mux, "GET", "/api/me/matches", nil, "u_list", "")
	require.Equal(t, http.StatusOK, w.Code)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	require.Len(t, page.Items, 2)
	require.Equal(t, "https://example.test/apply/o1", page.Items[0]["apply_url"])

	// Detail
	w2 := doMe(t, mux, "GET", "/api/me/matches/mo1", nil, "u_list", "")
	require.Equal(t, http.StatusOK, w2.Code)
	var detail map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &detail)
	require.Equal(t, "mo1", detail["match_id"])
	require.Equal(t, "https://example.test/apply/o1", detail["apply_url"])

	// Other candidate can't see it
	w3 := doMe(t, mux, "GET", "/api/me/matches/mo1", nil, "intruder", "")
	require.Equal(t, http.StatusNotFound, w3.Code)
}

func TestDismissAndView(t *testing.T) {
	mux, db, ctx := setupExtensionEnv(t)
	_, err := db.ExecContext(ctx, `
INSERT INTO candidate_matches (match_id, candidate_id, opportunity_id, status, score, last_event_id)
VALUES ('m_dv', 'u_dv', 'o_dv', 'new', 0.7, '')
`)
	require.NoError(t, err)

	// View (idempotent — viewed_at set on first call, preserved on second)
	w1 := doMe(t, mux, "POST", "/api/me/matches/m_dv/view", nil, "u_dv", "")
	require.Equal(t, http.StatusNoContent, w1.Code)
	w2 := doMe(t, mux, "POST", "/api/me/matches/m_dv/view", nil, "u_dv", "")
	require.Equal(t, http.StatusNoContent, w2.Code)

	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM engagement_events WHERE candidate_id='u_dv' AND opportunity_id='o_dv'`).Scan(&n))
	require.GreaterOrEqual(t, n, 2)

	// Dismiss
	w3 := doMe(t, mux, "POST", "/api/me/matches/m_dv/dismiss", nil, "u_dv", "k-dismiss")
	require.Equal(t, http.StatusOK, w3.Code)
	var detail map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &detail)
	require.Equal(t, "dismissed", detail["status"])

	// Dismiss again — idempotent (200 with same status, no extra event count)
	var beforeEvts int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM candidate_match_events WHERE candidate_id='u_dv' AND kind='dismissed'`).Scan(&beforeEvts)
	w4 := doMe(t, mux, "POST", "/api/me/matches/m_dv/dismiss", nil, "u_dv", "k-dismiss")
	require.Equal(t, http.StatusOK, w4.Code)
	var afterEvts int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM candidate_match_events WHERE candidate_id='u_dv' AND kind='dismissed'`).Scan(&afterEvts)
	require.Equal(t, beforeEvts, afterEvts, "idempotent replay must not duplicate events")
}

func TestRulesGetDefaultThenPut(t *testing.T) {
	mux, _, _ := setupExtensionEnv(t)
	// GET without prior PUT → default rules
	w := doMe(t, mux, "GET", "/api/me/rules", nil, "u_rules", "")
	require.Equal(t, http.StatusOK, w.Code)
	var got applications.Rules
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, applications.DefaultRules().Version, got.Version)

	// PUT new rules
	custom := applications.DefaultRules()
	custom.MinScore = 0.8
	custom.Countries = []string{"KE", "UG"}
	w2 := doMe(t, mux, "PUT", "/api/me/rules", custom, "u_rules", "k-rules")
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	var saved applications.Rules
	_ = json.Unmarshal(w2.Body.Bytes(), &saved)
	require.InDelta(t, 0.8, saved.MinScore, 1e-9)

	// GET round-trip
	w3 := doMe(t, mux, "GET", "/api/me/rules", nil, "u_rules", "")
	require.Equal(t, http.StatusOK, w3.Code)
	var rt applications.Rules
	_ = json.Unmarshal(w3.Body.Bytes(), &rt)
	require.InDelta(t, 0.8, rt.MinScore, 1e-9)
}

func TestProfileFieldsWithETag(t *testing.T) {
	mux, db, ctx := setupExtensionEnv(t)
	_, err := db.ExecContext(ctx,
		`INSERT INTO candidate_profiles (id, current_title, skills) VALUES ('u_pf', 'SWE', ARRAY['go','postgres']::text[])
		   ON CONFLICT DO NOTHING`)
	require.NoError(t, err)

	w := doMe(t, mux, "GET", "/api/me/profile-fields", nil, "u_pf", "")
	require.Equal(t, http.StatusOK, w.Code)
	etag := w.Header().Get("ETag")
	require.NotEmpty(t, etag)

	// Conditional GET → 304
	r := httptest.NewRequest("GET", "/api/me/profile-fields", nil)
	r.Header.Set("X-Candidate-ID", "u_pf")
	r.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	require.Equal(t, http.StatusNotModified, rec.Code)
}

func TestUnauthenticated(t *testing.T) {
	mux, _, _ := setupExtensionEnv(t)
	w := doMe(t, mux, "GET", "/api/me/matches", nil, "", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// unitVec returns a unit vector of length dim with 1.0 at axis.
func unitVec1024(axis int) []float32 {
	v := make([]float32, 1024)
	if axis >= 0 && axis < 1024 {
		v[axis] = 1
	} else {
		v[0] = 1
	}
	return v
}

func vectorLit(v []float32) string {
	out := "["
	for i, f := range v {
		if i > 0 {
			out += ","
		}
		out += strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return out + "]"
}

func seedProfile(t *testing.T, db *sql.DB, ctx context.Context, id, sub, plan string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
INSERT INTO candidate_profiles (id, subscription, plan_id)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET subscription = EXCLUDED.subscription, plan_id = EXCLUDED.plan_id`,
		id, sub, plan)
	require.NoError(t, err)
}

func seedIndex(t *testing.T, db *sql.DB, ctx context.Context, id string, emb []float32, kinds, countries []string, minScore float64) {
	t.Helper()
	idx := matching.NewIndexStore(db)
	require.NoError(t, idx.Upsert(ctx, matching.CandidateIndex{
		CandidateID: id,
		Embedding:   emb,
		MinScore:    minScore,
		DailyCap:    25,
		WeeklyCap:   100,
		Kinds:       kinds,
		Countries:   countries,
		Enabled:     true,
	}))
}

func seedOpp(t *testing.T, db *sql.DB, ctx context.Context, id string, emb []float32, kind, country string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
INSERT INTO opportunities (canonical_id, slug, kind, title, apply_url, posted_at, status, hidden, embedding, country, first_seen_at)
VALUES ($1::varchar(20), $1::text, $3, $1::text, 'https://example.test/apply/' || $1::text, now(), 'active', false, $2::vector, $4, now())
ON CONFLICT (canonical_id) DO NOTHING
`, id, vectorLit(emb), kind, country)
	require.NoError(t, err)
}

func TestRefreshMatches_NoEmbeddingIs409(t *testing.T) {
	mux, db, ctx := setupExtensionEnv(t)
	seedProfile(t, db, ctx, "u_refresh_none", "paid", "starter")
	// No candidate_match_indexes row → no_embedding
	w := doMe(t, mux, "POST", "/api/me/matches/refresh", nil, "u_refresh_none", "idem-refresh-none")
	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	require.Contains(t, w.Body.String(), "no_embedding")
}

func TestRefreshMatches_WithEmbeddingRecomputes(t *testing.T) {
	mux, db, ctx := setupExtensionEnv(t)
	const cand = "u_refresh_ok"
	seedProfile(t, db, ctx, cand, "paid", "starter")
	emb := unitVec1024(0)
	seedIndex(t, db, ctx, cand, emb, []string{"job"}, []string{"KE"}, 0.1)
	seedOpp(t, db, ctx, "opp_r1", emb, "job", "KE")
	seedOpp(t, db, ctx, "opp_r2", unitVec1024(1), "job", "KE") // far vector

	// Before refresh: no matches for this candidate
	w0 := doMe(t, mux, "GET", "/api/me/matches", nil, cand, "")
	require.Equal(t, http.StatusOK, w0.Code, "body=%s", w0.Body.String())
	var page0 struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w0.Body.Bytes(), &page0))
	require.Empty(t, page0.Items, "precondition: no matches before refresh")

	// Refresh → gap-fill writes matches
	w := doMe(t, mux, "POST", "/api/me/matches/refresh", nil, cand, "idem-refresh-ok")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var res map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, true, res["ok"])
	// At least the close opp should score above min
	written, _ := res["matches_written"].(float64)
	require.GreaterOrEqual(t, written, float64(1), "body=%v", res)

	// Re-list shows matches
	w2 := doMe(t, mux, "GET", "/api/me/matches", nil, cand, "")
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	var page struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &page))
	require.NotEmpty(t, page.Items, "matches must appear after refresh")
}

func TestNotificationsGetDefaultAndPut(t *testing.T) {
	mux, db, ctx := setupExtensionEnv(t)
	const cand = "u_notif"
	seedProfile(t, db, ctx, cand, "free", "")

	// GET without prior PUT → defaults
	w := doMe(t, mux, "GET", "/api/me/notifications", nil, cand, "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Contains(t, got, "email_digest")
	require.Contains(t, got, "match_alerts")

	// PUT new prefs
	body := map[string]any{
		"email_digest":     "daily",
		"match_alerts":     true,
		"weekly_summary":   false,
		"marketing_emails": true,
	}
	w2 := doMe(t, mux, "PUT", "/api/me/notifications", body, cand, "idem-notif")
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	var put map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &put))
	require.Equal(t, true, put["ok"])
	require.Equal(t, "daily", put["email_digest"])
	require.Equal(t, true, put["match_alerts"])

	// GET round-trip
	w3 := doMe(t, mux, "GET", "/api/me/notifications", nil, cand, "")
	require.Equal(t, http.StatusOK, w3.Code)
	var rt map[string]any
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &rt))
	require.Equal(t, "daily", rt["email_digest"])
	require.Equal(t, true, rt["match_alerts"])
	require.Equal(t, false, rt["weekly_summary"])
	require.Equal(t, true, rt["marketing_emails"])
}

// TestIterativeCycle_MatchMutateRulesRefreshRelist drives the sequential path
// required by verification criterion 5:
// seed profile+embedding+opp → list matches (empty) → refresh → list (non-empty)
// → mutate rules (min_score) → refresh again → re-list without 5xx.
func TestIterativeCycle_MatchMutateRulesRefreshRelist(t *testing.T) {
	mux, db, ctx := setupExtensionEnv(t)
	const cand = "u_iter"
	emb := unitVec1024(5)
	seedProfile(t, db, ctx, cand, "paid", "managed")
	seedIndex(t, db, ctx, cand, emb, []string{"job"}, []string{"UG"}, 0.05)
	seedOpp(t, db, ctx, "opp_i1", emb, "job", "UG")

	// 1) Initial list — empty
	wList0 := doMe(t, mux, "GET", "/api/me/matches", nil, cand, "")
	require.Equal(t, http.StatusOK, wList0.Code)
	var list0 struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(wList0.Body.Bytes(), &list0)
	require.Empty(t, list0.Items)

	// 2) First refresh — creates matches
	wRef1 := doMe(t, mux, "POST", "/api/me/matches/refresh", nil, cand, "iter-r1")
	require.Equal(t, http.StatusOK, wRef1.Code, "body=%s", wRef1.Body.String())
	var ref1 map[string]any
	require.NoError(t, json.Unmarshal(wRef1.Body.Bytes(), &ref1))
	require.Equal(t, true, ref1["ok"])
	require.GreaterOrEqual(t, ref1["matches_written"].(float64), float64(1))

	wList1 := doMe(t, mux, "GET", "/api/me/matches", nil, cand, "")
	require.Equal(t, http.StatusOK, wList1.Code)
	var list1 struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(wList1.Body.Bytes(), &list1))
	require.NotEmpty(t, list1.Items)
	n1 := len(list1.Items)

	// 3) Mutate preference rules (raise min_score)
	custom := applications.DefaultRules()
	custom.MinScore = 0.99 // very high — may filter future gap-fills
	custom.Countries = []string{"UG"}
	wRules := doMe(t, mux, "PUT", "/api/me/rules", custom, cand, "iter-rules")
	require.Equal(t, http.StatusOK, wRules.Code, "body=%s", wRules.Body.String())
	var rules applications.Rules
	require.NoError(t, json.Unmarshal(wRules.Body.Bytes(), &rules))
	require.InDelta(t, 0.99, rules.MinScore, 1e-9)

	// Persist index min_score to match rules so refresh uses new threshold
	_, err := db.ExecContext(ctx, `UPDATE candidate_match_indexes SET min_score = 0.99 WHERE candidate_id = $1`, cand)
	require.NoError(t, err)

	// 4) Second refresh — must not 5xx; may write 0 new matches at high threshold
	wRef2 := doMe(t, mux, "POST", "/api/me/matches/refresh", nil, cand, "iter-r2")
	require.Equal(t, http.StatusOK, wRef2.Code, "body=%s", wRef2.Body.String())
	var ref2 map[string]any
	require.NoError(t, json.Unmarshal(wRef2.Body.Bytes(), &ref2))
	require.Equal(t, true, ref2["ok"])
	require.InDelta(t, 0.99, ref2["min_score"].(float64), 1e-9)

	// 5) Re-list — still coherent (prior matches remain; no 5xx)
	wList2 := doMe(t, mux, "GET", "/api/me/matches", nil, cand, "")
	require.Equal(t, http.StatusOK, wList2.Code)
	var list2 struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &list2))
	require.GreaterOrEqual(t, len(list2.Items), n1, "prior matches must remain after iterative refresh")
}
