package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
)

type captureNotificationService struct {
	notificationv1connect.UnimplementedNotificationServiceHandler
	mu  sync.Mutex
	got []*notificationv1.Notification
	err error
}

func (c *captureNotificationService) Send(
	_ context.Context,
	req *connect.Request[notificationv1.SendRequest],
	stream *connect.ServerStream[notificationv1.SendResponse],
) error {
	if c.err != nil {
		return c.err
	}
	c.mu.Lock()
	c.got = append(c.got, req.Msg.GetData()...)
	c.mu.Unlock()
	// No stream items required for client drain.
	_ = stream
	return nil
}

func TestMatchesReadyQueuesViaNotificationService(t *testing.T) {
	cap := &captureNotificationService{}
	_, h := notificationv1connect.NewNotificationServiceHandler(cap)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := notificationv1connect.NewNotificationServiceClient(http.DefaultClient, srv.URL)
	n := New(client, nil, "https://opportunities.example.test/")

	n.MatchesReady(context.Background(), "cnd_1", "batch_1", []MatchItem{
		{CanonicalID: "opp_1", Title: "Backend Eng", Score: 0.9},
	}, true)

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.got) != 1 {
		t.Fatalf("notifications=%d, want 1", len(cap.got))
	}
	got := cap.got[0]
	if got.GetTemplate() != TemplateMatchesReady {
		t.Fatalf("template=%q", got.GetTemplate())
	}
	if got.GetRecipient().GetProfileId() != "cnd_1" {
		t.Fatalf("recipient=%q", got.GetRecipient().GetProfileId())
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(got.GetData()), &data); err != nil {
		t.Fatalf("data json: %v", err)
	}
	if data["delivery"] != "immediate" {
		t.Fatalf("delivery=%v, want immediate", data["delivery"])
	}
	if data["dashboard_url"] != "https://opportunities.example.test/dashboard/#matches" {
		t.Fatalf("dashboard_url=%v", data["dashboard_url"])
	}
}

func TestMatchesReadyDigestFlag(t *testing.T) {
	cap := &captureNotificationService{}
	_, h := notificationv1connect.NewNotificationServiceHandler(cap)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := notificationv1connect.NewNotificationServiceClient(http.DefaultClient, srv.URL)
	n := New(client, nil, "https://opportunities.example.test")

	n.MatchesReady(context.Background(), "cnd_2", "batch_2", []MatchItem{
		{CanonicalID: "opp_2"},
	}, false)

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.got) != 1 {
		t.Fatalf("notifications=%d, want 1", len(cap.got))
	}
	var data map[string]any
	_ = json.Unmarshal([]byte(cap.got[0].GetData()), &data)
	if data["delivery"] != "digest" {
		t.Fatalf("delivery=%v, want digest", data["delivery"])
	}
}

func TestNilClientNoPanic(t *testing.T) {
	n := New(nil, nil, "")
	// Must not panic when client is missing.
	n.MatchesReady(context.Background(), "cnd", "b", []MatchItem{{CanonicalID: "x"}}, true)
	n.MatchesDigest(context.Background(), "cnd", "b", []MatchItem{{CanonicalID: "x"}})
	n.WeeklyJobsDigest(context.Background(), "cnd", map[string]any{"n": 1})
	n.CVStaleNudge(context.Background(), "cnd", map[string]any{"days": 90})
}

func TestEmptyItemsSkipSend(t *testing.T) {
	cap := &captureNotificationService{}
	_, h := notificationv1connect.NewNotificationServiceHandler(cap)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := notificationv1connect.NewNotificationServiceClient(http.DefaultClient, srv.URL)
	n := New(client, nil, "")
	n.MatchesReady(context.Background(), "cnd", "b", nil, true)
	n.MatchesDigest(context.Background(), "cnd", "b", nil)
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.got) != 0 {
		t.Fatalf("expected no send for empty items, got %d", len(cap.got))
	}
}
