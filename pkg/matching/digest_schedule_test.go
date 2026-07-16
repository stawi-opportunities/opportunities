package matching_test

import (
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

func TestShouldSendDigest(t *testing.T) {
	t.Parallel()
	// 2026-07-13 is a Monday.
	monday := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	tuesday := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)

	onWeekly := matching.DigestPrefs{EmailDigest: "weekly", WeeklySummary: true, CommEmail: true}
	onDaily := matching.DigestPrefs{EmailDigest: "daily", WeeklySummary: true, CommEmail: true}
	off := matching.DigestPrefs{EmailDigest: "off", WeeklySummary: true, CommEmail: true}
	noSummary := matching.DigestPrefs{EmailDigest: "weekly", WeeklySummary: false, CommEmail: true}
	noEmail := matching.DigestPrefs{EmailDigest: "weekly", WeeklySummary: true, CommEmail: false}

	cases := []struct {
		name    string
		prefs   matching.DigestPrefs
		cadence string
		now     time.Time
		want    bool
	}{
		{"weekly auto monday", onWeekly, "auto", monday, true},
		{"weekly auto tuesday", onWeekly, "auto", tuesday, false},
		{"daily auto any day", onDaily, "auto", tuesday, true},
		{"off never", off, "auto", monday, false},
		{"summary off never", noSummary, "auto", monday, false},
		{"comm email off never", noEmail, "auto", monday, false},
		{"force daily only daily", onDaily, "daily", tuesday, true},
		{"force daily skips weekly", onWeekly, "daily", monday, false},
		{"force weekly only weekly", onWeekly, "weekly", tuesday, true},
		{"force weekly skips daily", onDaily, "weekly", monday, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := matching.ShouldSendDigest(tc.prefs, tc.cadence, tc.now, time.UTC, time.Monday)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeAndWeekday(t *testing.T) {
	t.Parallel()
	if matching.NormalizeDigestCadence("") != matching.DigestWeekly {
		t.Fatal("empty → weekly")
	}
	if matching.ParseWeekday("friday") != time.Friday {
		t.Fatal("friday")
	}
	if matching.LoadLocationOrUTC("Not/AZone") != time.UTC {
		t.Fatal("bad zone → UTC")
	}
}
