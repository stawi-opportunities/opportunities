//go:build integration

package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	v1 "github.com/stawi-opportunities/opportunities/apps/applications/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/tests/integration/testhelpers"
)

type ApplicationsLifecycleSuite struct {
	suite.Suite
	db  *sql.DB
	ctx context.Context
	mux *http.ServeMux
}

func TestApplicationsLifecycleSuite(t *testing.T) {
	suite.Run(t, new(ApplicationsLifecycleSuite))
}

func (s *ApplicationsLifecycleSuite) SetupSuite() {
	s.ctx = context.Background()
	s.db = testhelpers.PostgresContainerNoMigrate(s.T(), s.ctx)
	require.NoError(s.T(), testhelpers.EnsureOpportunitiesStub(s.ctx, s.db))
	testhelpers.ApplyMigrationsDir(s.T(), s.ctx, s.db, "../../db/migrations")

	store := applications.NewStore(s.db)
	events := applications.NewEventLog(s.db)
	notes := applications.NewNotesStore(s.db)
	reminders := applications.NewRemindersStore(s.db)
	attachments := applications.NewAttachmentsStore(s.db)
	idem := applications.NewIdempotencyStore(s.db, time.Hour)
	blobs := applications.NewMemoryBlobStore()

	s.mux = http.NewServeMux()
	v1.Mount(s.mux, &v1.Deps{
		Store:            store,
		EventLog:         events,
		NotesStore:       notes,
		RemindersStore:   reminders,
		AttachmentsStore: attachments,
		BlobStore:        blobs,
		Idempotency:      idem,
	})
}

func (s *ApplicationsLifecycleSuite) do(method, path string, body any, cand, idemKey string) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(s.T(), err)
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
	s.mux.ServeHTTP(w, r)
	return w
}

func (s *ApplicationsLifecycleSuite) TestFullStateMachineWalk() {
	cand := "c_walk"
	createW := s.do("POST", "/api/me/applications",
		map[string]any{"match_id": "m_walk", "opportunity_id": "o_walk"},
		cand, "create-1")
	require.Equal(s.T(), http.StatusCreated, createW.Code, "body=%s", createW.Body.String())
	var created map[string]any
	require.NoError(s.T(), json.Unmarshal(createW.Body.Bytes(), &created))
	appID, _ := created["application_id"].(string)
	require.NotEmpty(s.T(), appID)

	walk := []string{"applying", "submitted", "screening", "interview", "offer", "accepted"}
	for i, target := range walk {
		w := s.do("PATCH", "/api/me/applications/"+appID,
			map[string]any{"status": target}, cand, "")
		require.Equal(s.T(), http.StatusOK, w.Code,
			"step %d → %s body=%s", i, target, w.Body.String())
		var detail map[string]any
		require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &detail))
		require.Equal(s.T(), target, detail["status"], "step %d", i)
	}

	// Every transition should have produced an application_events row.
	var n int
	require.NoError(s.T(), s.db.QueryRowContext(s.ctx,
		`SELECT count(*) FROM application_events WHERE application_id = $1`, appID).Scan(&n))
	// Expect 7 events: 1 created + 6 state_changed.
	require.Equal(s.T(), 7, n)
}

func (s *ApplicationsLifecycleSuite) TestInvalidTransitionReturns409WithAllowed() {
	cand := "c_invalid"
	createW := s.do("POST", "/api/me/applications",
		map[string]any{"match_id": "m_inv", "opportunity_id": "o_inv"}, cand, "")
	require.Equal(s.T(), http.StatusCreated, createW.Code)
	var created map[string]any
	_ = json.Unmarshal(createW.Body.Bytes(), &created)
	appID, _ := created["application_id"].(string)

	w := s.do("PATCH", "/api/me/applications/"+appID,
		map[string]any{"status": "offer"}, cand, "")
	require.Equal(s.T(), http.StatusConflict, w.Code, "body=%s", w.Body.String())
	require.Equal(s.T(), "application/problem+json", w.Header().Get("Content-Type"))
	var p map[string]any
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &p))
	require.Equal(s.T(), "invalid_transition", p["title"])
	require.Equal(s.T(), "new", p["from"])
	require.Equal(s.T(), "offer", p["to"])
	allowed, _ := p["allowed"].([]any)
	require.NotEmpty(s.T(), allowed)
}

func (s *ApplicationsLifecycleSuite) TestIdempotentReplay() {
	cand := "c_idem"
	w1 := s.do("POST", "/api/me/applications",
		map[string]any{"match_id": "m_idem", "opportunity_id": "o_idem"},
		cand, "k-idem-1")
	require.Equal(s.T(), http.StatusCreated, w1.Code)
	body1 := w1.Body.Bytes()

	// Replay with the same key returns exactly the same response.
	w2 := s.do("POST", "/api/me/applications",
		map[string]any{"match_id": "m_idem", "opportunity_id": "o_idem"},
		cand, "k-idem-1")
	require.Equal(s.T(), http.StatusCreated, w2.Code)
	require.JSONEq(s.T(), string(body1), w2.Body.String())

	// Without the key, the second create hits the (cand, opp) unique
	// constraint and returns 409.
	w3 := s.do("POST", "/api/me/applications",
		map[string]any{"match_id": "m_idem", "opportunity_id": "o_idem"},
		cand, "")
	require.Equal(s.T(), http.StatusConflict, w3.Code)
}

func (s *ApplicationsLifecycleSuite) TestSubResourcesAttachToTheSameApplication() {
	cand := "c_sub"
	createW := s.do("POST", "/api/me/applications",
		map[string]any{"match_id": "m_sub", "opportunity_id": "o_sub"}, cand, "")
	require.Equal(s.T(), http.StatusCreated, createW.Code)
	var app map[string]any
	_ = json.Unmarshal(createW.Body.Bytes(), &app)
	appID, _ := app["application_id"].(string)

	// note
	n := s.do("POST", "/api/me/applications/"+appID+"/notes",
		map[string]any{"body": "shortlist reminder"}, cand, "k-note")
	require.Equal(s.T(), http.StatusCreated, n.Code, "body=%s", n.Body.String())

	// reminder
	r := s.do("POST", "/api/me/applications/"+appID+"/reminders",
		map[string]any{"due_at": time.Now().Add(24 * time.Hour).Format(time.RFC3339)},
		cand, "k-rem")
	require.Equal(s.T(), http.StatusCreated, r.Code, "body=%s", r.Body.String())

	// Every sub-resource emits its kind of event.
	rows, err := s.db.QueryContext(s.ctx,
		`SELECT kind FROM application_events WHERE application_id = $1 ORDER BY occurred_at`, appID)
	require.NoError(s.T(), err)
	defer func() { _ = rows.Close() }()
	got := []string{}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		got = append(got, k)
	}
	require.Contains(s.T(), got, "created")
	require.Contains(s.T(), got, "note_added")
	require.Contains(s.T(), got, "reminder_set")
}

func (s *ApplicationsLifecycleSuite) TestCrossCandidateIsolation() {
	candA := "c_a"
	candB := "c_b"
	createW := s.do("POST", "/api/me/applications",
		map[string]any{"match_id": "m_iso", "opportunity_id": "o_iso"}, candA, "")
	require.Equal(s.T(), http.StatusCreated, createW.Code)
	var app map[string]any
	_ = json.Unmarshal(createW.Body.Bytes(), &app)
	appID, _ := app["application_id"].(string)

	// B cannot read A's application.
	w1 := s.do("GET", "/api/me/applications/"+appID, nil, candB, "")
	require.Equal(s.T(), http.StatusNotFound, w1.Code)

	// B cannot patch A's application.
	w2 := s.do("PATCH", "/api/me/applications/"+appID,
		map[string]any{"status": "applying"}, candB, "")
	require.Equal(s.T(), http.StatusNotFound, w2.Code)

	// B's list endpoint must not return A's row.
	w3 := s.do("GET", "/api/me/applications", nil, candB, "")
	require.Equal(s.T(), http.StatusOK, w3.Code)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w3.Body.Bytes(), &page)
	require.Empty(s.T(), page.Items)
}
