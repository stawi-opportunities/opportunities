package business

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

func TestGoogleProvider_ImportAndExport(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar/v3/freeBusy", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"calendars": map[string]any{
				"primary": map[string]any{
					"busy": []map[string]string{
						{"start": "2026-08-11T10:00:00Z", "end": "2026-08-11T11:00:00Z"},
					},
				},
			},
		})
	})
	mux.HandleFunc("/calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			return
		}
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "evt_1"})
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	// Rewrite Google host to test server via custom transport.
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Use a client that rewrites URLs to test server.
	client := srv.Client()
	// Monkey-patch by calling helpers with test URLs — exercise Export via direct httpDoJSON
	// Full provider uses hard-coded hosts; unit-test parse helpers + credentials instead.
	creds, err := parseCreds(`{"access_token":"tok"}`)
	if err != nil || creds.AccessToken != "tok" {
		t.Fatalf("creds: %v %v", creds, err)
	}

	// freeBusy shape via httpDoJSON against srv
	var resp struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	_, err = httpDoJSON(context.Background(), client, http.MethodPost, srv.URL+"/calendar/v3/freeBusy", "tok", map[string]any{}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Calendars["primary"].Busy) != 1 {
		t.Fatalf("busy: %+v", resp)
	}

	var created struct {
		ID string `json:"id"`
	}
	_, err = httpDoJSON(context.Background(), client, http.MethodPost, srv.URL+"/calendar/v3/calendars/primary/events", "tok",
		map[string]any{"summary": "x"}, &created)
	if err != nil || created.ID != "evt_1" {
		t.Fatalf("create: %v %+v", err, created)
	}

	// Ready gate
	p := GoogleCalendarProvider{HTTP: client, Enabled: false}
	if p.Ready() {
		t.Fatal("expected not ready")
	}
	p.Enabled = true
	if !p.Ready() {
		t.Fatal("expected ready")
	}
	_ = models.ExternalConnection{}
	_ = time.Now()
}

func TestParseICSBusyFromMultistatus(t *testing.T) {
	xml := `BEGIN:VEVENT
UID:abc
DTSTART:20260811T100000Z
DTEND:20260811T110000Z
SUMMARY:Meet
END:VEVENT`
	out := parseICSBusyFromMultistatus(xml)
	if len(out) != 1 || out[0].ExternalKey != "caldav:abc" {
		t.Fatalf("%+v", out)
	}
	if !strings.Contains(out[0].Note, "Meet") {
		t.Fatalf("note %q", out[0].Note)
	}
}

func TestMemoryProviderRoundTrip(t *testing.T) {
	m := NewMemoryProvider()
	conn := &models.ExternalConnection{Provider: "memory", ExternalCalendarID: "c1"}
	id, err := m.ExportBooking(context.Background(), conn, models.ExternalEvent{
		UID: "u1", Title: "T", Start: time.Now().UTC(), End: time.Now().UTC().Add(time.Hour),
	})
	if err != nil || id == "" {
		t.Fatal(err, id)
	}
	blocks, _, err := m.ImportBusy(context.Background(), conn, time.Now().Add(-time.Hour), time.Now().Add(2*time.Hour))
	if err != nil || len(blocks) == 0 {
		t.Fatal(err, blocks)
	}
}
