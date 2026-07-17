package notify

import (
	"context"
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

func TestSend_ProfileStyleTemplateAndPayload(t *testing.T) {
	cap := &captureNotificationService{}
	_, h := notificationv1connect.NewNotificationServiceHandler(cap)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	cli := notificationv1connect.NewNotificationServiceClient(http.DefaultClient, srv.URL)
	err := Send(context.Background(), cli, Message{
		Template:  DefaultTemplateMatchesReady,
		ProfileID: "prof_1",
		Variables: map[string]any{
			"candidate_id": "cnd_1",
			"count":        float64(2),
			"code":         "123456",
		},
		Priority:    notificationv1.PRIORITY_HIGH,
		PrioritySet: true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.got) != 1 {
		t.Fatalf("got %d notifications", len(cap.got))
	}
	n := cap.got[0]
	if n.GetTemplate() != DefaultTemplateMatchesReady {
		t.Fatalf("template=%q", n.GetTemplate())
	}
	if n.GetRecipient().GetProfileId() != "prof_1" {
		t.Fatalf("profile_id=%q", n.GetRecipient().GetProfileId())
	}
	if n.GetRecipient().GetProfileType() != "Profile" {
		t.Fatalf("profile_type=%q", n.GetRecipient().GetProfileType())
	}
	if !n.GetOutBound() || !n.GetAutoRelease() {
		t.Fatalf("outbound/autorelease not set")
	}
	if n.GetPayload() == nil || n.GetPayload().AsMap()["code"] != "123456" {
		t.Fatalf("payload=%v", n.GetPayload())
	}
	if n.GetPriority() != notificationv1.PRIORITY_HIGH {
		t.Fatalf("priority=%v", n.GetPriority())
	}
}

func TestSend_NilClientError(t *testing.T) {
	err := Send(context.Background(), nil, Message{Template: "t", ProfileID: "p"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSend_RequiresTemplateAndProfile(t *testing.T) {
	cap := &captureNotificationService{}
	_, h := notificationv1connect.NewNotificationServiceHandler(cap)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cli := notificationv1connect.NewNotificationServiceClient(http.DefaultClient, srv.URL)

	if err := Send(context.Background(), cli, Message{ProfileID: "p"}); err == nil {
		t.Fatal("want template required")
	}
	if err := Send(context.Background(), cli, Message{Template: "t"}); err == nil {
		t.Fatal("want profile_id required")
	}
}

func TestOrDefault(t *testing.T) {
	if OrDefault("", "def") != "def" {
		t.Fatal()
	}
	if OrDefault("x", "def") != "x" {
		t.Fatal()
	}
}
