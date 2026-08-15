package models

import (
	"testing"
	"time"
)

func TestComputeSlots_BasicIntersect(t *testing.T) {
	rules := []WeekRule{}
	for d := 1; d <= 5; d++ {
		rules = append(rules, WeekRule{Weekday: d, Start: "09:00", End: "12:00"})
	}
	demands := []ResourceDemand{{
		ResourceID: "r1", Capacity: 1, Quantity: 1, Rules: rules, Timezone: "UTC",
	}}
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) // Monday
	end := start.AddDate(0, 0, 5)
	slots, err := ComputeSlots(demands, nil, start, end, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
}
