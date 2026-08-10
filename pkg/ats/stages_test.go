package ats

import "testing"

func TestValidateAdvance_happy(t *testing.T) {
	if err := ValidateAdvance(StageApplied, StageScreen); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAdvance_toRejected(t *testing.T) {
	if err := ValidateAdvance(StageApplied, StageRejected); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAdvance_illegal(t *testing.T) {
	if err := ValidateAdvance(StageApplied, StageOffer); err == nil {
		t.Fatal("expected error for skip")
	}
}

func TestValidateAdvance_terminal(t *testing.T) {
	if err := ValidateAdvance(StageHired, StageOffer); err == nil {
		t.Fatal("expected error from hired")
	}
	if !IsTerminal(StageHired) {
		t.Fatal("hired should be terminal")
	}
}

func TestDefaultStages(t *testing.T) {
	s := DefaultStages()
	if len(s) != 5 || s[0] != StageApplied || s[4] != StageHired {
		t.Fatalf("unexpected defaults: %v", s)
	}
}
