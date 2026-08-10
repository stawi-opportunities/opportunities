package models

import "testing"

func TestValidateAdvance(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{"happy", StageApplied, StageScreen, false},
		{"reject", StageApplied, StageRejected, false},
		{"skip illegal", StageApplied, StageOffer, true},
		{"terminal", StageHired, StageOffer, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAdvance(tt.from, tt.to)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
