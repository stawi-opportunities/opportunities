package ats

import (
	"fmt"
	"time"
)

// BusyInterval is a blocked time range (UTC instants).
type BusyInterval struct {
	Start time.Time
	End   time.Time
}

// Slot is a candidate bookable window (UTC).
type Slot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// SlotOverlaps reports whether two half-open intervals [a,b) overlap.
func SlotOverlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// ParseClock parses "HH:MM" into hour and minute.
func ParseClock(s string) (hour, min int, err error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, 0, fmt.Errorf("ats: clock %q: %w", s, err)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("ats: clock %q out of range", s)
	}
	return h, m, nil
}

// ComputeSlots returns open slots of durationMin in [windowStart, windowEnd)
// for the intersection of all panel members' weekly rules, minus exceptions and busy.
// rulesByProfile must include every panelist; empty rules for a profile yields no slots.
func ComputeSlots(
	loc *time.Location,
	rulesByProfile map[string][]WeekRule,
	exceptionsByProfile map[string][]ExceptionDay,
	busy []BusyInterval,
	windowStart, windowEnd time.Time,
	durationMin int,
) ([]Slot, error) {
	if durationMin <= 0 {
		return nil, fmt.Errorf("ats: duration must be positive")
	}
	if loc == nil {
		loc = time.UTC
	}
	if len(rulesByProfile) == 0 {
		return nil, fmt.Errorf("ats: no panel availability")
	}
	for pid, rules := range rulesByProfile {
		if len(rules) == 0 {
			return nil, fmt.Errorf("ats: empty availability for profile %s", pid)
		}
	}

	dur := time.Duration(durationMin) * time.Minute
	// Build per-day free windows in local time, then intersect across profiles.
	var slots []Slot
	day := time.Date(windowStart.In(loc).Year(), windowStart.In(loc).Month(), windowStart.In(loc).Day(), 0, 0, 0, 0, loc)
	endDay := windowEnd.In(loc)
	for !day.After(endDay) {
		dayStr := day.Format("2006-01-02")
		// Intersect free intervals for this local day across all profiles.
		var free [][2]time.Time
		first := true
		blockedDay := false
		for pid, rules := range rulesByProfile {
			if dayBlocked(exceptionsByProfile[pid], dayStr) {
				blockedDay = true
				break
			}
			var profFree [][2]time.Time
			for _, r := range rules {
				if r.Weekday != int(day.Weekday()) {
					continue
				}
				sh, sm, err := ParseClock(r.Start)
				if err != nil {
					return nil, err
				}
				eh, em, err := ParseClock(r.End)
				if err != nil {
					return nil, err
				}
				start := time.Date(day.Year(), day.Month(), day.Day(), sh, sm, 0, 0, loc)
				end := time.Date(day.Year(), day.Month(), day.Day(), eh, em, 0, 0, loc)
				if !end.After(start) {
					continue
				}
				profFree = append(profFree, [2]time.Time{start, end})
			}
			if first {
				free = profFree
				first = false
			} else {
				free = intersectIntervals(free, profFree)
			}
		}
		if blockedDay || first {
			day = day.AddDate(0, 0, 1)
			continue
		}
		for _, iv := range free {
			for t := iv[0]; t.Add(dur).Before(iv[1]) || t.Add(dur).Equal(iv[1]); t = t.Add(dur) {
				s, e := t, t.Add(dur)
				if e.Before(windowStart) || !s.Before(windowEnd) {
					continue
				}
				if s.Before(windowStart) {
					continue
				}
				if busyOverlaps(busy, s, e) {
					continue
				}
				slots = append(slots, Slot{Start: s.UTC(), End: e.UTC()})
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return slots, nil
}

func dayBlocked(ex []ExceptionDay, dayStr string) bool {
	for _, e := range ex {
		if e.Date == dayStr && e.Blocked {
			return true
		}
	}
	return false
}

func busyOverlaps(busy []BusyInterval, s, e time.Time) bool {
	for _, b := range busy {
		if SlotOverlaps(s, e, b.Start, b.End) {
			return true
		}
	}
	return false
}

func intersectIntervals(a, b [][2]time.Time) [][2]time.Time {
	var out [][2]time.Time
	for _, x := range a {
		for _, y := range b {
			start := x[0]
			if y[0].After(start) {
				start = y[0]
			}
			end := x[1]
			if y[1].Before(end) {
				end = y[1]
			}
			if end.After(start) {
				out = append(out, [2]time.Time{start, end})
			}
		}
	}
	return out
}
