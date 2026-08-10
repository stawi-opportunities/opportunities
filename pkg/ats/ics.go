package ats

import (
	"fmt"
	"strings"
	"time"
)

// BuildICS returns a minimal VCALENDAR for a scheduled interview.
func BuildICS(iv *Interview, jobTitle, candidateProfile string, organizerEmail string) string {
	if iv == nil || iv.SlotStart == nil || iv.SlotEnd == nil {
		return ""
	}
	uid := iv.ICSUID
	if uid == "" {
		uid = iv.ID + "@stawi-ats"
	}
	summary := "Interview"
	if jobTitle != "" {
		summary = "Interview: " + jobTitle
	}
	desc := fmt.Sprintf("Application %s\\nCandidate profile %s\\nType %s",
		iv.ApplicationID, candidateProfile, iv.Type)
	loc := iv.Location
	if iv.VideoURL != "" {
		if loc != "" {
			loc += " / "
		}
		loc += iv.VideoURL
	}
	if organizerEmail == "" {
		organizerEmail = "noreply@stawi.local"
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Stawi//ATS//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("METHOD:REQUEST\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString("UID:" + uid + "\r\n")
	b.WriteString("DTSTAMP:" + formatICSTime(time.Now().UTC()) + "\r\n")
	b.WriteString("DTSTART:" + formatICSTime(iv.SlotStart.UTC()) + "\r\n")
	b.WriteString("DTEND:" + formatICSTime(iv.SlotEnd.UTC()) + "\r\n")
	b.WriteString("SUMMARY:" + icsEscape(summary) + "\r\n")
	b.WriteString("DESCRIPTION:" + icsEscape(desc) + "\r\n")
	if loc != "" {
		b.WriteString("LOCATION:" + icsEscape(loc) + "\r\n")
	}
	b.WriteString("ORGANIZER:mailto:" + organizerEmail + "\r\n")
	b.WriteString("STATUS:CONFIRMED\r\n")
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func formatICSTime(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

func icsEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `;`, `\;`)
	s = strings.ReplaceAll(s, `,`, `\,`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
