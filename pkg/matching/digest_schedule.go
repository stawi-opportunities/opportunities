package matching

import (
	"strings"
	"time"
)

// Digest cadence values stored on candidate_profiles.email_digest.
const (
	DigestDaily      = "daily"
	DigestTwiceDaily = "twice_daily"
	DigestWeekly     = "weekly"
	DigestOff        = "off"
)

// NormalizeDigestCadence maps free-form input to daily|twice_daily|weekly|off.
// Empty / unknown defaults to weekly (product default for paid subscribers).
func NormalizeDigestCadence(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case DigestDaily:
		return DigestDaily
	case DigestTwiceDaily, "twice-daily", "bidaily":
		return DigestTwiceDaily
	case DigestOff, "none", "disabled":
		return DigestOff
	case DigestWeekly, "":
		return DigestWeekly
	default:
		return DigestWeekly
	}
}

// DigestPrefs is the subset of notification settings that gate summary emails.
type DigestPrefs struct {
	EmailDigest   string
	WeeklySummary bool
	// CommEmail when false means the user opted out of email channel entirely.
	CommEmail bool
}

// DefaultDigestPrefs returns product defaults for a new / unset profile.
func DefaultDigestPrefs() DigestPrefs {
	return DigestPrefs{
		EmailDigest:   DigestWeekly,
		WeeklySummary: true,
		CommEmail:     true,
	}
}

// ShouldSendDigest reports whether this profile should receive a summary on
// this run. cadence is the run mode: "daily", "twice_daily", "weekly", or "auto".
//
//	auto         — honour email_digest; weekly only fires on weeklyWeekday in loc;
//	               twice_daily only fires when local hour is in [8,10) or [17,19)
//	daily        — only daily subscribers
//	twice_daily  — only twice_daily subscribers
//	weekly       — only weekly subscribers (ignore weekday)
func ShouldSendDigest(prefs DigestPrefs, cadence string, now time.Time, loc *time.Location, weeklyWeekday time.Weekday) bool {
	if !prefs.WeeklySummary {
		return false
	}
	if !prefs.CommEmail {
		return false
	}
	freq := NormalizeDigestCadence(prefs.EmailDigest)
	if freq == DigestOff {
		return false
	}

	mode := strings.ToLower(strings.TrimSpace(cadence))
	if mode == "" {
		mode = "auto"
	}

	switch mode {
	case DigestDaily:
		return freq == DigestDaily
	case DigestTwiceDaily:
		return freq == DigestTwiceDaily
	case DigestWeekly:
		return freq == DigestWeekly
	default: // auto
		if loc == nil {
			loc = time.UTC
		}
		local := now.In(loc)
		if freq == DigestDaily {
			return true
		}
		if freq == DigestTwiceDaily {
			h := local.Hour()
			return (h >= 8 && h < 10) || (h >= 17 && h < 19)
		}
		// weekly
		return local.Weekday() == weeklyWeekday
	}
}

// ParseWeekday accepts "monday"/ "1" / "Mon" etc. Default Monday.
func ParseWeekday(raw string) time.Weekday {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "0", "sun", "sunday":
		return time.Sunday
	case "1", "mon", "monday", "":
		return time.Monday
	case "2", "tue", "tues", "tuesday":
		return time.Tuesday
	case "3", "wed", "wednesday":
		return time.Wednesday
	case "4", "thu", "thur", "thurs", "thursday":
		return time.Thursday
	case "5", "fri", "friday":
		return time.Friday
	case "6", "sat", "saturday":
		return time.Saturday
	default:
		return time.Monday
	}
}

// LoadLocationOrUTC returns the named zone or UTC on error/empty.
func LoadLocationOrUTC(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}
