package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

type fakeActiveLister struct {
	members []DigestAudienceMember
	err     error
}

func (f *fakeActiveLister) ListActive(context.Context) ([]DigestAudienceMember, error) {
	return f.members, f.err
}

type fakeIndexReader struct {
	byID map[string]*matching.CandidateIndex
}

func (f *fakeIndexReader) Get(_ context.Context, id string) (*matching.CandidateIndex, error) {
	if ci, ok := f.byID[id]; ok {
		return ci, nil
	}
	return nil, matching.ErrNotFound
}

type fakeUnseenSource struct {
	mu       sync.Mutex
	top      []matching.DigestMatch
	listErr  error
	receipts []matching.DigestMatch
	candIDs  []string
}

func (f *fakeUnseenSource) ListTopUnseenMatchesForDigest(_ context.Context, candidateID, channel string, limit int) ([]matching.DigestMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	_ = candidateID
	_ = channel
	if limit <= 0 || limit > 3 {
		limit = 3
	}
	out := f.top
	if len(out) > limit {
		out = out[:limit]
	}
	// Return a copy so callers cannot mutate the fixture.
	cp := make([]matching.DigestMatch, len(out))
	copy(cp, out)
	return cp, nil
}

func (f *fakeUnseenSource) InsertNotificationReceipts(_ context.Context, candidateID, channel string, items []matching.DigestMatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candIDs = append(f.candIDs, candidateID)
	f.receipts = append(f.receipts, items...)
	_ = channel
	return nil
}

type captureNotificationService struct {
	notificationv1connect.UnimplementedNotificationServiceHandler
	mu  sync.Mutex
	got []*notificationv1.Notification
}

func (c *captureNotificationService) Send(
	_ context.Context,
	req *connect.Request[notificationv1.SendRequest],
	_ *connect.ServerStream[notificationv1.SendResponse],
) error {
	c.mu.Lock()
	c.got = append(c.got, req.Msg.GetData()...)
	c.mu.Unlock()
	return nil
}

func newAdminSvc(t *testing.T) (context.Context, *frame.Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("matches-digest-test"),
		frametests.WithNoopDriver(),
	)
	t.Cleanup(func() { svc.Stop(ctx) })
	return ctx, svc
}

func entitled(id string) DigestAudienceMember {
	return DigestAudienceMember{
		ID: id, EmailDigest: "weekly", WeeklySummary: true, CommEmail: true,
	}
}

func embedIdx(id string) *matching.CandidateIndex {
	return &matching.CandidateIndex{CandidateID: id, Embedding: []float32{0.1, 0.2}}
}

// Candidates without an index row (no embedding yet) are skipped, not
// failed — MatchInvoke is never invoked so the nil KNN/Store is safe.
func TestMatchesWeeklyDigestSkipsCandidatesWithoutIndex(t *testing.T) {
	_, svc := newAdminSvc(t)
	// Force weekly cadence so preference filter doesn't skip first.
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:            svc,
		Active:         &fakeActiveLister{members: []DigestAudienceMember{entitled("cnd_1"), entitled("cnd_2")}},
		Index:          &fakeIndexReader{byID: map[string]*matching.CandidateIndex{}},
		DefaultCadence: "weekly",
		WeeklyWeekday:  time.Monday,
		Location:       time.UTC,
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Audience != 2 || resp.Skipped != 2 || resp.Matched != 0 || resp.Failed != 0 {
		t.Fatalf("resp=%+v, want audience=2 skipped=2 matched=0 failed=0", resp)
	}
}

// An index row with an empty embedding is also skipped.
func TestMatchesWeeklyDigestSkipsEmptyEmbedding(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:            svc,
		Active:         &fakeActiveLister{members: []DigestAudienceMember{entitled("cnd_1")}},
		Index:          &fakeIndexReader{byID: map[string]*matching.CandidateIndex{"cnd_1": {CandidateID: "cnd_1", Embedding: nil}}},
		DefaultCadence: "weekly",
		WeeklyWeekday:  time.Monday,
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Skipped != 1 || resp.Matched != 0 {
		t.Fatalf("resp=%+v, want skipped=1 matched=0", resp)
	}
}

func TestMatchesDigestSkipsOffPreference(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc: svc,
		Active: &fakeActiveLister{members: []DigestAudienceMember{
			{ID: "off", EmailDigest: "off", WeeklySummary: true, CommEmail: true},
		}},
		Index: &fakeIndexReader{byID: map[string]*matching.CandidateIndex{
			"off": {CandidateID: "off", Embedding: []float32{0.1}},
		}},
		DefaultCadence: "auto",
		WeeklyWeekday:  time.Monday,
		Location:       time.UTC,
		Now:            func() time.Time { return time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC) }, // Monday
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Skipped != 1 {
		t.Fatalf("resp=%+v want skipped=1 for off preference", resp)
	}
}

func TestMatchesDigestSkipsTwiceDailyOutsideWindow(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc: svc,
		Active: &fakeActiveLister{members: []DigestAudienceMember{
			{ID: "td", EmailDigest: "twice_daily", WeeklySummary: true, CommEmail: true},
		}},
		Index:          &fakeIndexReader{byID: map[string]*matching.CandidateIndex{"td": embedIdx("td")}},
		DefaultCadence: "auto",
		Location:       time.UTC,
		// Midday is outside [8,10) and [17,19).
		Now: func() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) },
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Skipped != 1 || resp.Matched != 0 {
		t.Fatalf("resp=%+v want skipped=1 for twice_daily outside window", resp)
	}
}

func TestMatchesWeeklyDigestListActiveError(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:    svc,
		Active: &fakeActiveLister{err: errors.New("db wedged")},
		Index:  &fakeIndexReader{},
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", rec.Code)
	}
}

func TestMatchesWeeklyDigestRejectsNonPost(t *testing.T) {
	_, svc := newAdminSvc(t)
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:    svc,
		Active: &fakeActiveLister{},
		Index:  &fakeIndexReader{},
	})
	req := httptest.NewRequest(http.MethodGet, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", rec.Code)
	}
}

func TestNewRepoActiveCandidateLister(t *testing.T) {
	called := false
	l := NewRepoActiveCandidateLister(func(_ context.Context, limit int) ([]DigestAudienceMember, error) {
		called = true
		if limit != 5000 {
			t.Fatalf("limit=%d, want default 5000", limit)
		}
		return []DigestAudienceMember{{ID: "a"}, {ID: "b"}}, nil
	}, 0)
	ids, err := l.ListActive(context.Background())
	if err != nil || !called || len(ids) != 2 {
		t.Fatalf("ListActive: ids=%v err=%v called=%v", ids, err, called)
	}
}

// Skips send when there are no unseen matches (already receipted or empty).
func TestMatchesDigestSkipsWhenNoUnseen(t *testing.T) {
	_, svc := newAdminSvc(t)
	unseen := &fakeUnseenSource{top: nil}
	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:            svc,
		Active:         &fakeActiveLister{members: []DigestAudienceMember{entitled("cnd_1")}},
		Index:          &fakeIndexReader{byID: map[string]*matching.CandidateIndex{"cnd_1": embedIdx("cnd_1")}},
		Unseen:         unseen,
		DefaultCadence: "weekly",
		WeeklyWeekday:  time.Monday,
		Location:       time.UTC,
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Skipped != 1 || resp.Matched != 0 {
		t.Fatalf("resp=%+v want skipped=1 matched=0 when no unseen", resp)
	}
	if len(unseen.receipts) != 0 {
		t.Fatalf("receipts=%v want none", unseen.receipts)
	}
}

// Sends at most top-3 and records receipts after successful notify.
func TestMatchesDigestSendsTop3AndRecordsReceipts(t *testing.T) {
	_, svc := newAdminSvc(t)
	// Five unseen fixtures; handler must cap to 3.
	unseen := &fakeUnseenSource{top: []matching.DigestMatch{
		{MatchID: "m1", OpportunityID: "o1", Score: 0.95, Title: "A"},
		{MatchID: "m2", OpportunityID: "o2", Score: 0.90, Title: "B"},
		{MatchID: "m3", OpportunityID: "o3", Score: 0.85, Title: "C"},
		{MatchID: "m4", OpportunityID: "o4", Score: 0.80, Title: "D"},
		{MatchID: "m5", OpportunityID: "o5", Score: 0.75, Title: "E"},
	}}
	cap := &captureNotificationService{}
	_, h := notificationv1connect.NewNotificationServiceHandler(cap)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cli := notificationv1connect.NewNotificationServiceClient(http.DefaultClient, srv.URL)

	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:             svc,
		Active:          &fakeActiveLister{members: []DigestAudienceMember{entitled("cnd_1")}},
		Index:           &fakeIndexReader{byID: map[string]*matching.CandidateIndex{"cnd_1": embedIdx("cnd_1")}},
		Unseen:          unseen,
		NotificationCli: cli,
		Templates:       notify.Templates{},
		DefaultCadence:  "weekly",
		WeeklyWeekday:   time.Monday,
		Location:        time.UTC,
		PublicSiteURL:   "https://example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Matched != 1 || resp.Skipped != 0 || resp.Failed != 0 {
		t.Fatalf("resp=%+v want matched=1", resp)
	}
	unseen.mu.Lock()
	defer unseen.mu.Unlock()
	if len(unseen.receipts) != 3 {
		t.Fatalf("receipts=%d want 3; items=%+v", len(unseen.receipts), unseen.receipts)
	}
	if unseen.receipts[0].MatchID != "m1" || unseen.receipts[2].MatchID != "m3" {
		t.Fatalf("receipt match ids=%+v", unseen.receipts)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.got) != 1 {
		t.Fatalf("notifications=%d want 1", len(cap.got))
	}
	payload := cap.got[0].GetPayload().AsMap()
	matches, ok := payload["matches"].([]any)
	if !ok {
		t.Fatalf("payload matches type %T", payload["matches"])
	}
	if len(matches) != 3 {
		t.Fatalf("payload matches len=%d want 3", len(matches))
	}
	if payload["count"] != float64(3) {
		t.Fatalf("count=%v want 3", payload["count"])
	}
}

// Second-style run: after receipts, list returns empty → skip (no re-send).
func TestMatchesDigestDoesNotResendReceipted(t *testing.T) {
	_, svc := newAdminSvc(t)
	// Simulate store after first send: no unseen left.
	unseen := &fakeUnseenSource{top: nil}
	cap := &captureNotificationService{}
	_, h := notificationv1connect.NewNotificationServiceHandler(cap)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cli := notificationv1connect.NewNotificationServiceClient(http.DefaultClient, srv.URL)

	handler := MatchesWeeklyDigestHandler(MatchesWeeklyDigestDeps{
		Svc:             svc,
		Active:          &fakeActiveLister{members: []DigestAudienceMember{entitled("cnd_1")}},
		Index:           &fakeIndexReader{byID: map[string]*matching.CandidateIndex{"cnd_1": embedIdx("cnd_1")}},
		Unseen:          unseen,
		NotificationCli: cli,
		DefaultCadence:  "weekly",
		WeeklyWeekday:   time.Monday,
		Location:        time.UTC,
	})
	req := httptest.NewRequest(http.MethodPost, "/_admin/matches/weekly_digest", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	var resp matchesWeeklyDigestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Skipped != 1 || resp.Matched != 0 {
		t.Fatalf("resp=%+v want skip when all receipted", resp)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.got) != 0 {
		t.Fatalf("notifications=%d want 0", len(cap.got))
	}
}
