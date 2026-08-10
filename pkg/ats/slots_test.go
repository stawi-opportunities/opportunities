package ats

import (
	"testing"
	"time"
)

func TestSlotOverlaps(t *testing.T) {
	s1 := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	e1 := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	s2 := time.Date(2026, 8, 10, 9, 30, 0, 0, time.UTC)
	e2 := time.Date(2026, 8, 10, 10, 30, 0, 0, time.UTC)
	if !SlotOverlaps(s1, e1, s2, e2) {
		t.Fatal("expected overlap")
	}
	s3 := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	e3 := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	if SlotOverlaps(s1, e1, s3, e3) {
		t.Fatal("adjacent should not overlap (half-open)")
	}
}

func TestComputeSlots_singlePanelist(t *testing.T) {
	// Monday 2026-08-10 is a Monday.
	loc := time.UTC
	// time.Monday = 1
	rules := map[string][]WeekRule{
		"p1": {{Weekday: int(time.Monday), Start: "09:00", End: "12:00"}},
	}
	winStart := time.Date(2026, 8, 10, 0, 0, 0, 0, loc)
	winEnd := time.Date(2026, 8, 11, 0, 0, 0, 0, loc)
	slots, err := ComputeSlots(loc, rules, nil, nil, winStart, winEnd, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 3 {
		t.Fatalf("want 3 hourly slots, got %d: %v", len(slots), slots)
	}
}

func TestComputeSlots_emptyProfile(t *testing.T) {
	rules := map[string][]WeekRule{"p1": {}, "p2": {{Weekday: 1, Start: "09:00", End: "10:00"}}}
	_, err := ComputeSlots(time.UTC, rules, nil, nil,
		time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		30)
	if err == nil {
		t.Fatal("expected error for empty availability")
	}
}

func TestComputeSlots_busyBlocks(t *testing.T) {
	loc := time.UTC
	rules := map[string][]WeekRule{
		"p1": {{Weekday: int(time.Monday), Start: "09:00", End: "11:00"}},
	}
	busy := []BusyInterval{{
		Start: time.Date(2026, 8, 10, 9, 0, 0, 0, loc),
		End:   time.Date(2026, 8, 10, 10, 0, 0, 0, loc),
	}}
	slots, err := ComputeSlots(loc, rules, nil, busy,
		time.Date(2026, 8, 10, 0, 0, 0, 0, loc),
		time.Date(2026, 8, 11, 0, 0, 0, 0, loc),
		60)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatalf("want 1 slot after busy, got %d", len(slots))
	}
	if slots[0].Start.Hour() != 10 {
		t.Fatalf("want 10:00 slot, got %v", slots[0].Start)
	}
}
