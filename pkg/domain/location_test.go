package domain

import "testing"

func TestLocation_IsZero(t *testing.T) {
	if !(Location{}).IsZero() {
		t.Error("zero Location should report IsZero")
	}
	if (Location{Country: "KE"}).IsZero() {
		t.Error("Location with Country should not report IsZero")
	}
}

func TestLocation_HasCoords(t *testing.T) {
	if (Location{}).HasCoords() {
		t.Error("zero coords should not report HasCoords")
	}
	if !(Location{Lat: -1.286, Lon: 36.817}).HasCoords() {
		t.Error("Nairobi coords should report HasCoords")
	}
	if (Location{Lat: 0, Lon: 0}).HasCoords() {
		t.Error("0,0 should be treated as unset")
	}
}
