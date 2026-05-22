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

	v1 "github.com/stawi-opportunities/opportunities/apps/applications/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

func setupHandlerEnv(t *testing.T) (*http.ServeMux, *applications.Store, *applications.EventLog, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.PostgresContainerNoMigrate(t, ctx)
	require.NoError(t, testhelpers.EnsureOpportunitiesStub(ctx, db))
	testhelpers.ApplyMigrationsDir(t, ctx, db, "../../../../../db/migrations")
	store := applications.NewStore(db)
	log := applications.NewEventLog(db)
	mux := http.NewServeMux()
	v1.Mount(mux, &v1.Deps{
		Store:            store,
		EventLog:         log,
		NotesStore:       applications.NewNotesStore(db),
		RemindersStore:   applications.NewRemindersStore(db),
		AttachmentsStore: applications.NewAttachmentsStore(db),
		BlobStore:        applications.NewMemoryBlobStore(),
		Idempotency:      applications.NewIdempotencyStore(db, time.Hour),
	})
	return mux, store, log, db, ctx
}

func doReq(t *testing.T, mux *http.ServeMux, method, path string, body any, candidate, idempotencyKey string) *httptest.ResponseRecorder {
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
	if candidate != "" {
		r.Header.Set("X-Candidate-ID", candidate)
	}
	if idempotencyKey != "" {
		r.Header.Set("Idempotency-Key", idempotencyKey)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestApplicationsHandlers_FullFlow(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)

	// Create
	w := doReq(t, mux, "POST", "/api/me/applications", map[string]any{
		"match_id":       "m_1",
		"opportunity_id": "o_1",
	}, "cand_x", "k-1")
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	appID, _ := created["application_id"].(string)
	require.NotEmpty(t, appID)

	// Idempotent replay
	w2 := doReq(t, mux, "POST", "/api/me/applications", map[string]any{
		"match_id":       "m_1",
		"opportunity_id": "o_1",
	}, "cand_x", "k-1")
	require.Equal(t, http.StatusCreated, w2.Code)
	require.JSONEq(t, w.Body.String(), w2.Body.String())

	// Patch to applying
	w3 := doReq(t, mux, "PATCH", "/api/me/applications/"+appID, map[string]any{
		"status": "applying",
	}, "cand_x", "k-patch-1")
	require.Equal(t, http.StatusOK, w3.Code, "body=%s", w3.Body.String())

	// Get returns events array
	w4 := doReq(t, mux, "GET", "/api/me/applications/"+appID, nil, "cand_x", "")
	require.Equal(t, http.StatusOK, w4.Code)
	var detail map[string]any
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &detail))
	evts, _ := detail["events"].([]any)
	require.GreaterOrEqual(t, len(evts), 2) // created + state_changed
}

func TestApplicationsHandlers_NotAuthenticated(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)
	w := doReq(t, mux, "POST", "/api/me/applications", map[string]any{"match_id": "m", "opportunity_id": "o"}, "", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestApplicationsHandlers_OtherCandidateRowIsNotFound(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)
	w := doReq(t, mux, "POST", "/api/me/applications", map[string]any{"match_id": "m_2", "opportunity_id": "o_2"}, "cand_a", "")
	require.Equal(t, http.StatusCreated, w.Code)
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	appID, _ := created["application_id"].(string)

	// Different candidate tries to read — gets 404, not 200.
	w2 := doReq(t, mux, "GET", "/api/me/applications/"+appID, nil, "cand_b", "")
	require.Equal(t, http.StatusNotFound, w2.Code)
}

func TestApplicationsHandlers_InvalidTransitionReturns409(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)
	w := doReq(t, mux, "POST", "/api/me/applications", map[string]any{"match_id": "m_3", "opportunity_id": "o_3"}, "c", "")
	require.Equal(t, http.StatusCreated, w.Code)
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	appID, _ := created["application_id"].(string)

	w2 := doReq(t, mux, "PATCH", "/api/me/applications/"+appID, map[string]any{"status": "offer"}, "c", "")
	require.Equal(t, http.StatusConflict, w2.Code, "body=%s", w2.Body.String())
	require.Equal(t, "application/problem+json", w2.Header().Get("Content-Type"))
}

func TestApplicationsHandlers_AppendEvent(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)
	w := doReq(t, mux, "POST", "/api/me/applications", map[string]any{"match_id": "m_4", "opportunity_id": "o_4"}, "c", "")
	require.Equal(t, http.StatusCreated, w.Code)
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	appID, _ := created["application_id"].(string)

	w2 := doReq(t, mux, "POST", "/api/me/applications/"+appID+"/events",
		map[string]any{"kind": "submission_succeeded", "data": map[string]any{"ats": "greenhouse"}},
		"c", "k-evt-1")
	require.Equal(t, http.StatusCreated, w2.Code)
}
