package models

import (
	"fmt"
	"time"
)

// SlotOverlaps reports whether two half-open intervals overlap.
func SlotOverlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// ParseClock parses "HH:MM".
func ParseClock(s string) (hour, min int, err error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, 0, fmt.Errorf("calendar: clock %q: %w", s, err)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("calendar: clock %q out of range", s)
	}
	return h, m, nil
}

// ResourceDemand is capacity needed on a resource for slot search.
type ResourceDemand struct {
	ResourceID string
	Capacity   int // resource max capacity
	Quantity   int // demand for this search
	Rules      []WeekRule
	Exceptions []ExceptionDay
	Timezone   string
}

// ComputeSlots finds intervals where all resources have free capacity.
// busyByResource maps resource_id → intervals that consume 1 unit each (or use BusyUsage).
func ComputeSlots(
	demands []ResourceDemand,
	busyByResource map[string][]BusyInterval,
	windowStart, windowEnd time.Time,
	durationMin int,
) ([]Slot, error) {
	if durationMin <= 0 {
		return nil, fmt.Errorf("%w: duration must be positive", ErrInvalid)
	}
	if len(demands) == 0 {
		return nil, fmt.Errorf("%w: no resources", ErrInvalid)
	}
	for _, d := range demands {
		if d.Quantity <= 0 {
			d.Quantity = 1
		}
		if d.Capacity <= 0 {
			d.Capacity = 1
		}
		if d.Quantity > d.Capacity {
			return nil, fmt.Errorf("%w: quantity exceeds capacity for %s", ErrInvalid, d.ResourceID)
		}
		if len(d.Rules) == 0 {
			return nil, fmt.Errorf("%w: empty availability for resource %s", ErrInvalid, d.ResourceID)
		}
	}

	// Use first resource timezone as grid anchor (or UTC).
	loc := time.UTC
	if demands[0].Timezone != "" {
		if l, err := time.LoadLocation(demands[0].Timezone); err == nil {
			loc = l
		}
	}

	dur := time.Duration(durationMin) * time.Minute
	var slots []Slot
	day := time.Date(windowStart.In(loc).Year(), windowStart.In(loc).Month(), windowStart.In(loc).Day(), 0, 0, 0, 0, loc)
	endDay := windowEnd.In(loc)

	for !day.After(endDay) {
		dayStr := day.Format("2006-01-02")
		// Intersect free windows for the day across all resources.
		var free [][2]time.Time
		first := true
		blocked := false
		for _, d := range demands {
			if dayBlocked(d.Exceptions, dayStr) {
				blocked = true
				break
			}
			var profFree [][2]time.Time
			resLoc := loc
			if d.Timezone != "" {
				if l, err := time.LoadLocation(d.Timezone); err == nil {
					resLoc = l
				}
			}
			// Rules are evaluated in resource local day matching weekday of `day` in loc —
			// use weekday of day in resource location for consistency.
			localDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, resLoc)
			for _, r := range d.Rules {
				if r.Weekday != int(localDay.Weekday()) {
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
				start := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), sh, sm, 0, 0, resLoc)
				end := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), eh, em, 0, 0, resLoc)
				if !end.After(start) {
					continue
				}
				profFree = append(profFree, [2]time.Time{start.UTC(), end.UTC()})
			}
			if first {
				free = profFree
				first = false
			} else {
				free = intersectWindows(free, profFree)
			}
		}
		if blocked || len(free) == 0 {
			day = day.AddDate(0, 0, 1)
			continue
		}
		// Candidate slots on grid (15 min).
		for _, w := range free {
			for t := w[0]; t.Add(dur).Before(w[1]) || t.Add(dur).Equal(w[1]); t = t.Add(15 * time.Minute) {
				s, e := t, t.Add(dur)
				if e.After(w[1]) || !s.Before(windowEnd) || !e.After(windowStart) {
					continue
				}
				if s.Before(windowStart) {
					continue
				}
				if allHaveCapacity(demands, busyByResource, s, e) {
					slots = append(slots, Slot{Start: s, End: e})
				}
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

func intersectWindows(a, b [][2]time.Time) [][2]time.Time {
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

func allHaveCapacity(demands []ResourceDemand, busy map[string][]BusyInterval, start, end time.Time) bool {
	for _, d := range demands {
		qty := d.Quantity
		if qty <= 0 {
			qty = 1
		}
		cap := d.Capacity
		if cap <= 0 {
			cap = 1
		}
		used := 0
		for _, b := range busy[d.ResourceID] {
			if SlotOverlaps(start, end, b.Start, b.End) {
				used++
			}
		}
		if used+qty > cap {
			return false
		}
	}
	return true
}

// HasCapacity reports whether a single interval is free for the given demands.
func HasCapacity(demands []ResourceDemand, busy map[string][]BusyInterval, start, end time.Time) bool {
	return allHaveCapacity(demands, busy, start, end)
}
