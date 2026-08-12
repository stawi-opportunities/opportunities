package models

import (
	"testing"
	"time"
)

func TestComputeSlots(t *testing.T) {
	loc := time.UTC
	rules := map[string][]WeekRule{
		"p1": {{Weekday: int(time.Monday), Start: "09:00", End: "12:00"}},
	}
	winStart := time.Date(2026, 8, 10, 0, 0, 0, 0, loc) // Monday
	winEnd := time.Date(2026, 8, 11, 0, 0, 0, 0, loc)
	slots, err := ComputeSlots(loc, rules, nil, nil, winStart, winEnd, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 3 {
		t.Fatalf("want 3 slots got %d", len(slots))
	}
}
