package models

import (
	"fmt"
	"strings"
	"time"
)

// BuildBookingICS returns a VCALENDAR for a confirmed booking.
func BuildBookingICS(b *Booking, resourceNames []string) string {
	if b == nil || b.StartAt.IsZero() || b.EndAt.IsZero() {
		return ""
	}
	uid := b.ICSUID
	if uid == "" {
		uid = b.ID + "@stawi-calendar"
	}
	summary := b.Title
	if summary == "" {
		summary = "Booking"
	}
	desc := b.Description
	if len(resourceNames) > 0 {
		desc += "\\nResources: " + strings.Join(resourceNames, ", ")
	}
	if b.Source != "" {
		desc += "\\nSource: " + b.Source
		if b.SourceRef != "" {
			desc += " / " + b.SourceRef
		}
	}
	loc := b.Location
	var sb strings.Builder
	sb.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Stawi//Calendar//EN\r\n")
	sb.WriteString("CALSCALE:GREGORIAN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\n")
	sb.WriteString("UID:" + uid + "\r\n")
	sb.WriteString("DTSTAMP:" + formatICSTime(time.Now().UTC()) + "\r\n")
	sb.WriteString("DTSTART:" + formatICSTime(b.StartAt.UTC()) + "\r\n")
	sb.WriteString("DTEND:" + formatICSTime(b.EndAt.UTC()) + "\r\n")
	sb.WriteString("SUMMARY:" + escapeICS(summary) + "\r\n")
	if desc != "" {
		sb.WriteString("DESCRIPTION:" + escapeICS(desc) + "\r\n")
	}
	if loc != "" {
		sb.WriteString("LOCATION:" + escapeICS(loc) + "\r\n")
	}
	sb.WriteString("STATUS:CONFIRMED\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	return sb.String()
}

func formatICSTime(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

func escapeICS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// ExternalEvent is a provider-neutral export payload.
type ExternalEvent struct {
	UID         string
	Title       string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	// ExternalEventID set when updating existing remote event.
	ExternalEventID string
}

func BookingToExternalEvent(b *Booking, line *BookingLine) ExternalEvent {
	if b == nil {
		return ExternalEvent{}
	}
	ev := ExternalEvent{
		UID:         b.ICSUID,
		Title:       b.Title,
		Description: b.Description,
		Location:    b.Location,
		Start:       b.StartAt,
		End:         b.EndAt,
	}
	if ev.UID == "" {
		ev.UID = b.ID
	}
	if line != nil {
		ev.ExternalEventID = line.ExternalEventID
	}
	if ev.Title == "" {
		ev.Title = fmt.Sprintf("Booking %s", b.ID)
	}
	return ev
}
