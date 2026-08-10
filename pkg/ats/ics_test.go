package ats

import (
	"strings"
	"testing"
	"time"
)

func TestBuildICS(t *testing.T) {
	start := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	iv := &Interview{
		ApplicationID: "app1", Type: "screen",
		ICSUID: "uid-1", SlotStart: &start, SlotEnd: &end,
		VideoURL: "https://meet.example/x",
	}
	iv.ID = "iv1"
	ics := BuildICS(iv, "Backend Eng", "cand-1", "recruiter@acme.test")
	for _, want := range []string{
		"BEGIN:VCALENDAR", "UID:uid-1", "SUMMARY:Interview: Backend Eng",
		"DTSTART:20260811T100000Z", "LOCATION:https://meet.example/x",
	} {
		if !strings.Contains(ics, want) {
			t.Fatalf("missing %q in:\n%s", want, ics)
		}
	}
}
