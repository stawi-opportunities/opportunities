//go:build integration

package v1_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/me/v1"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupExtensionEnv(t *testing.T) (*http.ServeMux, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	testhelpers.ApplyGreenfieldSchema(t, ctx, db)

	mux := http.NewServeMux()
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
	}, nil)
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
