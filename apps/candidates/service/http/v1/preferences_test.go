package v1

import (
	"bytes"
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

type prefCollector struct {
	mu  sync.Mutex
	got []eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
}

func (c *prefCollector) Name() string     { return eventsv1.TopicCandidatePreferencesUpdated }
func (c *prefCollector) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *prefCollector) Validate(context.Context, any) error { return nil }
func (c *prefCollector) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *prefCollector) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }

func TestPreferencesHandlerEmitsEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("prefs-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	col := &prefCollector{}
	svc.EventsManager().Add(col)
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	handler := PreferencesHandler(svc)

	body := map[string]any{
		"candidate_id":        "cnd_1",
		"remote_preference":   "remote",
		"salary_min":          80000,
		"salary_max":          140000,
		"currency":            "USD",
		"preferred_locations": []string{"KE"},
		"target_roles":        []string{"backend-engineer"},
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/candidates/preferences", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if col.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if col.Len() != 1 {
		t.Fatalf("emitted=%d, want 1", col.Len())
	}
	p := col.got[0].Payload
	if p.CandidateID != "cnd_1" || p.SalaryMin != 80000 {
		t.Fatalf("bad payload: %+v", p)
	}
}

func TestPreferencesHandlerRejectsMissingCandidateID(t *testing.T) {
	ctx, svc := frame.NewServiceWithContext(context.Background(),
		frame.WithName("prefs-empty"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	handler := PreferencesHandler(svc)
	raw := []byte(`{"salary_min":50000}`)
	req := httptest.NewRequest(http.MethodPost, "/candidates/preferences", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
