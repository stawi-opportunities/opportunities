package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/frametests"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type fakeStaleLister struct {
	stale []StaleCandidate
}

func (f *fakeStaleLister) ListStale(_ context.Context, _ time.Time) ([]StaleCandidate, error) {
	return f.stale, nil
}

type nudgeCollector struct {
	mu  sync.Mutex
	got []json.RawMessage
}

func (c *nudgeCollector) Name() string     { return eventsv1.TopicCandidateCVStaleNudge }
func (c *nudgeCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *nudgeCollector) Validate(context.Context, any) error { return nil }
func (c *nudgeCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	c.mu.Lock()
	c.got = append(c.got, append(json.RawMessage(nil), *raw...))
	c.mu.Unlock()
	return nil
}
func (c *nudgeCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestCVStaleNudgeHandlerEmitsOneEventPerStaleCandidate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("stale-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &nudgeCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	handler := CVStaleNudgeHandler(CVStaleNudgeDeps{
		Svc: svc,
		Lister: &fakeStaleLister{stale: []StaleCandidate{
			{CandidateID: "cnd_a", LastUploadAt: time.Now().Add(-70 * 24 * time.Hour)},
			{CandidateID: "cnd_b", LastUploadAt: time.Now().Add(-90 * 24 * time.Hour)},
		}},
		StaleAfter: 60 * 24 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/_admin/cv/stale_nudge", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 2 {
		t.Fatalf("emitted=%d, want 2", col.Len())
	}
}
