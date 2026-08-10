package tests

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

// FakeInterviewCalendar is an in-process InterviewCalendar for ATS tests.
// It mirrors service_calendar behaviour (availability → slots → bookings)
// without a network hop.
type FakeInterviewCalendar struct {
	mu    sync.Mutex
	rules map[string]struct {
		tz string
		r  []models.WeekRule
		ex []models.ExceptionDay
	}
	busy map[string][]models.BusyInterval // resourceID (= profileID)
}

func NewFakeInterviewCalendar() *FakeInterviewCalendar {
	return &FakeInterviewCalendar{
		rules: map[string]struct {
			tz string
			r  []models.WeekRule
			ex []models.ExceptionDay
		}{},
		busy: map[string][]models.BusyInterval{},
	}
}

func (f *FakeInterviewCalendar) EnsurePanelResources(_ context.Context, profileIDs []string) ([]string, error) {
	out := make([]string, 0, len(profileIDs))
	for _, p := range profileIDs {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: empty panel", models.ErrInvalid)
	}
	return out, nil
}

func (f *FakeInterviewCalendar) SyncProfileAvailability(
	_ context.Context, profileID, timezone string,
	rules []models.WeekRule, exceptions []models.ExceptionDay,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if timezone == "" {
		timezone = "UTC"
	}
	f.rules[profileID] = struct {
		tz string
		r  []models.WeekRule
		ex []models.ExceptionDay
	}{tz: timezone, r: append([]models.WeekRule(nil), rules...), ex: append([]models.ExceptionDay(nil), exceptions...)}
	return nil
}

func (f *FakeInterviewCalendar) GetProfileAvailability(_ context.Context, profileID string) (string, []models.WeekRule, []models.ExceptionDay, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.rules[profileID]
	if !ok {
		return "UTC", nil, nil, nil
	}
	return v.tz, append([]models.WeekRule(nil), v.r...), append([]models.ExceptionDay(nil), v.ex...), nil
}

func (f *FakeInterviewCalendar) ListPanelSlots(
	_ context.Context, resourceIDs []string, durationMin int, windowStart, windowEnd time.Time,
) ([]models.Slot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if durationMin <= 0 {
		durationMin = 30
	}
	if windowStart.IsZero() {
		windowStart = time.Now().UTC()
	}
	if windowEnd.IsZero() {
		windowEnd = windowStart.AddDate(0, 0, 14)
	}
	rulesBy := map[string][]models.WeekRule{}
	exBy := map[string][]models.ExceptionDay{}
	tzName := "UTC"
	var busy []models.BusyInterval
	for _, id := range resourceIDs {
		v, ok := f.rules[id]
		if !ok || len(v.r) == 0 {
			return nil, fmt.Errorf("%w: profile %s", models.ErrEmptyAvail, id)
		}
		rulesBy[id] = v.r
		exBy[id] = v.ex
		if v.tz != "" {
			tzName = v.tz
		}
		busy = append(busy, f.busy[id]...)
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	return models.ComputeSlots(loc, rulesBy, exBy, busy, windowStart.In(loc), windowEnd.In(loc), durationMin)
}

func (f *FakeInterviewCalendar) BookPanel(
	_ context.Context, resourceIDs []string, start, end time.Time, interviewID, _ string,
) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Conflict if any resource busy
	for _, id := range resourceIDs {
		for _, b := range f.busy[id] {
			if models.SlotOverlaps(start, end, b.Start, b.End) {
				return "", fmt.Errorf("%w: slot not available", models.ErrConflict)
			}
		}
	}
	for _, id := range resourceIDs {
		f.busy[id] = append(f.busy[id], models.BusyInterval{Start: start, End: end})
	}
	id := "fake_book_" + interviewID
	if id == "fake_book_" {
		id = "fake_book_" + util.IDString()
	}
	return id, nil
}

func (f *FakeInterviewCalendar) CancelInterviewBooking(_ context.Context, bookingID string) error {
	_ = bookingID
	// Simplified: tests don't re-free intervals.
	return nil
}
