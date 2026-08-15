package business

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

// credentialsJSON is the expected shape stored on ExternalConnection.CredentialsJSON.
// Products/OAuth callbacks write short-lived access tokens here (or refresh material).
type credentialsJSON struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	// CalDAV basic auth fallback.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// CalDAV principal / calendar home URL override.
	BaseURL string `json:"base_url,omitempty"`
}

func parseCreds(raw string) (credentialsJSON, error) {
	var c credentialsJSON
	if strings.TrimSpace(raw) == "" {
		return c, fmt.Errorf("calendar: credentials_json empty")
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, fmt.Errorf("calendar: credentials_json: %w", err)
	}
	return c, nil
}

func httpDoJSON(ctx context.Context, client *http.Client, method, rawURL, bearer string, body any, out any) (int, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("calendar: http %s %s → %d: %s", method, rawURL, resp.StatusCode, truncate(string(data), 300))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("calendar: decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// GoogleCalendarProvider implements free/busy import + event export via Google Calendar API v3.
// credentials_json: {"access_token":"…"} (OAuth user or service account access token).
type GoogleCalendarProvider struct {
	HTTP *http.Client
	// Enabled when env GOOGLE_CALENDAR_ENABLED and client configured.
	Enabled bool
}

func (g GoogleCalendarProvider) Name() string { return models.ProviderGoogle }
func (g GoogleCalendarProvider) Ready() bool  { return g.Enabled }

func (g GoogleCalendarProvider) calendarID(conn *models.ExternalConnection) string {
	if conn != nil && conn.ExternalCalendarID != "" {
		return conn.ExternalCalendarID
	}
	return "primary"
}

func (g GoogleCalendarProvider) ImportBusy(ctx context.Context, conn *models.ExternalConnection, from, to time.Time) ([]ImportedBusy, string, error) {
	if !g.Ready() {
		return nil, "", fmt.Errorf("calendar: google provider not enabled")
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return nil, "", err
	}
	if creds.AccessToken == "" {
		return nil, "", fmt.Errorf("calendar: google access_token required")
	}
	calID := url.PathEscape(g.calendarID(conn))
	body := map[string]any{
		"timeMin": from.UTC().Format(time.RFC3339),
		"timeMax": to.UTC().Format(time.RFC3339),
		"items":   []map[string]string{{"id": g.calendarID(conn)}},
	}
	var resp struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	if _, err := httpDoJSON(ctx, g.HTTP, http.MethodPost,
		"https://www.googleapis.com/calendar/v3/freeBusy",
		creds.AccessToken, body, &resp); err != nil {
		return nil, "", err
	}
	var out []ImportedBusy
	keyCal := g.calendarID(conn)
	for _, b := range resp.Calendars[keyCal].Busy {
		st, e1 := time.Parse(time.RFC3339, b.Start)
		en, e2 := time.Parse(time.RFC3339, b.End)
		if e1 != nil || e2 != nil {
			continue
		}
		out = append(out, ImportedBusy{
			Start: st, End: en,
			ExternalKey: fmt.Sprintf("google:%s:%s:%s", calID, b.Start, b.End),
			Note:        "google freeBusy",
		})
	}
	// Also list events as busy (covers all-day / tentative if freeBusy sparse).
	q := url.Values{}
	q.Set("timeMin", from.UTC().Format(time.RFC3339))
	q.Set("timeMax", to.UTC().Format(time.RFC3339))
	q.Set("singleEvents", "true")
	q.Set("maxResults", "250")
	var events struct {
		Items []struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
			Start   struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"end"`
			Status string `json:"status"`
		} `json:"items"`
		NextSyncToken string `json:"nextSyncToken"`
	}
	listURL := "https://www.googleapis.com/calendar/v3/calendars/" + calID + "/events?" + q.Encode()
	if _, err := httpDoJSON(ctx, g.HTTP, http.MethodGet, listURL, creds.AccessToken, nil, &events); err == nil {
		for _, it := range events.Items {
			if it.Status == "cancelled" {
				continue
			}
			st, en, ok := parseGoogleBounds(it.Start.DateTime, it.Start.Date, it.End.DateTime, it.End.Date)
			if !ok {
				continue
			}
			out = append(out, ImportedBusy{
				Start: st, End: en,
				ExternalKey: "google:event:" + it.ID,
				Note:        it.Summary,
			})
		}
		return out, events.NextSyncToken, nil
	}
	return out, time.Now().UTC().Format(time.RFC3339), nil
}

func parseGoogleBounds(startDT, startDate, endDT, endDate string) (time.Time, time.Time, bool) {
	var st, en time.Time
	var err error
	if startDT != "" {
		st, err = time.Parse(time.RFC3339, startDT)
	} else if startDate != "" {
		st, err = time.Parse("2006-01-02", startDate)
	} else {
		return time.Time{}, time.Time{}, false
	}
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if endDT != "" {
		en, err = time.Parse(time.RFC3339, endDT)
	} else if endDate != "" {
		en, err = time.Parse("2006-01-02", endDate)
	} else {
		return time.Time{}, time.Time{}, false
	}
	if err != nil || !en.After(st) {
		return time.Time{}, time.Time{}, false
	}
	return st, en, true
}

func (g GoogleCalendarProvider) ExportBooking(ctx context.Context, conn *models.ExternalConnection, event models.ExternalEvent) (string, error) {
	if !g.Ready() {
		return "", fmt.Errorf("calendar: google provider not enabled")
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return "", err
	}
	calID := url.PathEscape(g.calendarID(conn))
	payload := map[string]any{
		"summary":     event.Title,
		"description": event.Description,
		"location":    event.Location,
		"start":       map[string]string{"dateTime": event.Start.UTC().Format(time.RFC3339), "timeZone": "UTC"},
		"end":         map[string]string{"dateTime": event.End.UTC().Format(time.RFC3339), "timeZone": "UTC"},
	}
	if event.ExternalEventID != "" {
		var updated struct {
			ID string `json:"id"`
		}
		u := "https://www.googleapis.com/calendar/v3/calendars/" + calID + "/events/" + url.PathEscape(event.ExternalEventID)
		if _, err := httpDoJSON(ctx, g.HTTP, http.MethodPatch, u, creds.AccessToken, payload, &updated); err != nil {
			return "", err
		}
		if updated.ID != "" {
			return updated.ID, nil
		}
		return event.ExternalEventID, nil
	}
	var created struct {
		ID string `json:"id"`
	}
	u := "https://www.googleapis.com/calendar/v3/calendars/" + calID + "/events"
	if _, err := httpDoJSON(ctx, g.HTTP, http.MethodPost, u, creds.AccessToken, payload, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (g GoogleCalendarProvider) DeleteExport(ctx context.Context, conn *models.ExternalConnection, externalEventID string) error {
	if !g.Ready() || externalEventID == "" {
		return nil
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return err
	}
	calID := url.PathEscape(g.calendarID(conn))
	u := "https://www.googleapis.com/calendar/v3/calendars/" + calID + "/events/" + url.PathEscape(externalEventID)
	_, err = httpDoJSON(ctx, g.HTTP, http.MethodDelete, u, creds.AccessToken, nil, nil)
	return err
}

// MicrosoftCalendarProvider uses Microsoft Graph calendarView + events.
// credentials_json: {"access_token":"…"}.
type MicrosoftCalendarProvider struct {
	HTTP    *http.Client
	Enabled bool
}

func (m MicrosoftCalendarProvider) Name() string { return models.ProviderMicrosoft }
func (m MicrosoftCalendarProvider) Ready() bool  { return m.Enabled }

func (m MicrosoftCalendarProvider) calendarPath(conn *models.ExternalConnection) string {
	// default: primary calendar of signed-in user
	if conn != nil && conn.ExternalCalendarID != "" && conn.ExternalCalendarID != "primary" {
		return "/me/calendars/" + url.PathEscape(conn.ExternalCalendarID)
	}
	return "/me/calendar"
}

func (m MicrosoftCalendarProvider) ImportBusy(ctx context.Context, conn *models.ExternalConnection, from, to time.Time) ([]ImportedBusy, string, error) {
	if !m.Ready() {
		return nil, "", fmt.Errorf("calendar: microsoft provider not enabled")
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return nil, "", err
	}
	// calendarView
	q := url.Values{}
	q.Set("startDateTime", from.UTC().Format(time.RFC3339))
	q.Set("endDateTime", to.UTC().Format(time.RFC3339))
	q.Set("$select", "id,subject,start,end,isCancelled")
	q.Set("$top", "100")
	path := "https://graph.microsoft.com/v1.0" + m.calendarPath(conn) + "/calendarView?" + q.Encode()
	var resp struct {
		Value []struct {
			ID          string `json:"id"`
			Subject     string `json:"subject"`
			IsCancelled bool   `json:"isCancelled"`
			Start       struct {
				DateTime string `json:"dateTime"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
			} `json:"end"`
		} `json:"value"`
	}
	if _, err := httpDoJSON(ctx, m.HTTP, http.MethodGet, path, creds.AccessToken, nil, &resp); err != nil {
		return nil, "", err
	}
	var out []ImportedBusy
	for _, it := range resp.Value {
		if it.IsCancelled {
			continue
		}
		st, e1 := parseGraphDateTime(it.Start.DateTime)
		en, e2 := parseGraphDateTime(it.End.DateTime)
		if e1 != nil || e2 != nil || !en.After(st) {
			continue
		}
		out = append(out, ImportedBusy{
			Start: st, End: en,
			ExternalKey: "ms:event:" + it.ID,
			Note:        it.Subject,
		})
	}
	return out, time.Now().UTC().Format(time.RFC3339), nil
}

func parseGraphDateTime(s string) (time.Time, error) {
	// Graph often returns without Z: "2026-08-10T09:00:00.0000000"
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05.9999999", s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse("2006-01-02T15:04:05", s)
}

func (m MicrosoftCalendarProvider) ExportBooking(ctx context.Context, conn *models.ExternalConnection, event models.ExternalEvent) (string, error) {
	if !m.Ready() {
		return "", fmt.Errorf("calendar: microsoft provider not enabled")
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"subject":  event.Title,
		"body":     map[string]string{"contentType": "text", "content": event.Description},
		"start":    map[string]string{"dateTime": event.Start.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
		"end":      map[string]string{"dateTime": event.End.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
		"location": map[string]string{"displayName": event.Location},
	}
	base := "https://graph.microsoft.com/v1.0" + m.calendarPath(conn) + "/events"
	if event.ExternalEventID != "" {
		var updated struct {
			ID string `json:"id"`
		}
		u := "https://graph.microsoft.com/v1.0/me/events/" + url.PathEscape(event.ExternalEventID)
		if _, err := httpDoJSON(ctx, m.HTTP, http.MethodPatch, u, creds.AccessToken, payload, &updated); err != nil {
			return "", err
		}
		if updated.ID != "" {
			return updated.ID, nil
		}
		return event.ExternalEventID, nil
	}
	var created struct {
		ID string `json:"id"`
	}
	if _, err := httpDoJSON(ctx, m.HTTP, http.MethodPost, base, creds.AccessToken, payload, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (m MicrosoftCalendarProvider) DeleteExport(ctx context.Context, conn *models.ExternalConnection, externalEventID string) error {
	if !m.Ready() || externalEventID == "" {
		return nil
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return err
	}
	u := "https://graph.microsoft.com/v1.0/me/events/" + url.PathEscape(externalEventID)
	_, err = httpDoJSON(ctx, m.HTTP, http.MethodDelete, u, creds.AccessToken, nil, nil)
	return err
}

// CalDAVProvider performs basic REPORT free-busy and PUT/DELETE VEVENT over HTTP.
// credentials_json: {"base_url":"https://…/calendars/user/default/","username":"…","password":"…"}
// or access_token as Bearer.
type CalDAVProvider struct {
	HTTP    *http.Client
	Enabled bool
}

func (c CalDAVProvider) Name() string { return models.ProviderCalDAV }
func (c CalDAVProvider) Ready() bool  { return c.Enabled }

func (c CalDAVProvider) ImportBusy(ctx context.Context, conn *models.ExternalConnection, from, to time.Time) ([]ImportedBusy, string, error) {
	if !c.Ready() {
		return nil, "", fmt.Errorf("calendar: caldav provider not enabled")
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return nil, "", err
	}
	base := creds.BaseURL
	if base == "" {
		base = conn.ExternalCalendarID
	}
	if base == "" {
		return nil, "", fmt.Errorf("calendar: caldav base_url or external_calendar_id required")
	}
	// Prefer REPORT calendar-query for VEVENT time-range (widely supported).
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop><d:getetag/><c:calendar-data/></d:prop>
  <c:filter>
    <c:comp-filter name="VCALENDAR">
      <c:comp-filter name="VEVENT">
        <c:time-range start="%s" end="%s"/>
      </c:comp-filter>
    </c:comp-filter>
  </c:filter>
</c:calendar-query>`, from.UTC().Format("20060102T150405Z"), to.UTC().Format("20060102T150405Z"))
	req, err := http.NewRequestWithContext(ctx, "REPORT", strings.TrimRight(base, "/")+"/", strings.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")
	c.applyAuth(req, creds)
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("calendar: caldav REPORT %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	// Minimal parse: extract DTSTART/DTEND/UID pairs from calendar-data blobs.
	out := parseICSBusyFromMultistatus(string(data))
	return out, time.Now().UTC().Format(time.RFC3339), nil
}

func (c CalDAVProvider) applyAuth(req *http.Request, creds credentialsJSON) {
	if creds.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		return
	}
	if creds.Username != "" {
		req.SetBasicAuth(creds.Username, creds.Password)
	}
}

func parseICSBusyFromMultistatus(xmlBody string) []ImportedBusy {
	// Split on VEVENT blocks in embedded ICS.
	parts := strings.Split(xmlBody, "BEGIN:VEVENT")
	var out []ImportedBusy
	for i := 1; i < len(parts); i++ {
		block := parts[i]
		if idx := strings.Index(block, "END:VEVENT"); idx >= 0 {
			block = block[:idx]
		}
		uid := icsField(block, "UID")
		st := parseICSDate(icsField(block, "DTSTART"))
		en := parseICSDate(icsField(block, "DTEND"))
		if st.IsZero() || en.IsZero() || !en.After(st) {
			continue
		}
		if uid == "" {
			uid = st.Format(time.RFC3339) + en.Format(time.RFC3339)
		}
		out = append(out, ImportedBusy{
			Start: st, End: en, ExternalKey: "caldav:" + uid, Note: icsField(block, "SUMMARY"),
		})
	}
	return out
}

func icsField(block, name string) string {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(line, name+":") || strings.HasPrefix(line, name+";") {
			if i := strings.Index(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

func parseICSDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse("20060102T150405Z", s); err == nil {
		return t
	}
	if t, err := time.Parse("20060102T150405", s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("20060102", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func (c CalDAVProvider) ExportBooking(ctx context.Context, conn *models.ExternalConnection, event models.ExternalEvent) (string, error) {
	if !c.Ready() {
		return "", fmt.Errorf("calendar: caldav provider not enabled")
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return "", err
	}
	base := creds.BaseURL
	if base == "" {
		base = conn.ExternalCalendarID
	}
	uid := event.ExternalEventID
	if uid == "" {
		uid = event.UID
	}
	if uid == "" {
		uid = fmt.Sprintf("%d@stawi-calendar", time.Now().UnixNano())
	}
	// Sanitize filename
	file := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '@' || r == '.' {
			return r
		}
		return '-'
	}, uid) + ".ics"
	ics := models.BuildBookingICS(&models.Booking{
		Title: event.Title, Description: event.Description, Location: event.Location,
		StartAt: event.Start, EndAt: event.End, ICSUID: uid,
	}, nil)
	putURL := strings.TrimRight(base, "/") + "/" + file
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, strings.NewReader(ics))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	c.applyAuth(req, creds)
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("calendar: caldav PUT %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	return uid, nil
}

func (c CalDAVProvider) DeleteExport(ctx context.Context, conn *models.ExternalConnection, externalEventID string) error {
	if !c.Ready() || externalEventID == "" {
		return nil
	}
	creds, err := parseCreds(conn.CredentialsJSON)
	if err != nil {
		return err
	}
	base := creds.BaseURL
	if base == "" {
		base = conn.ExternalCalendarID
	}
	file := externalEventID
	if !strings.HasSuffix(file, ".ics") {
		file += ".ics"
	}
	putURL := strings.TrimRight(base, "/") + "/" + file
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, putURL, nil)
	if err != nil {
		return err
	}
	c.applyAuth(req, creds)
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 404 {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("calendar: caldav DELETE %d", resp.StatusCode)
	}
	return nil
}
