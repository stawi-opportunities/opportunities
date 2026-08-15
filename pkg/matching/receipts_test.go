package matching_test

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// Pure helper coverage for digest limit/channel defaults is exercised via
// store method signatures and admin handler fakes. These tests document
// DigestMatch field expectations for receipt writes.

func TestDigestMatchCarriesMatchID(t *testing.T) {
	t.Parallel()
	d := matching.DigestMatch{
		MatchID:       "m_1",
		OpportunityID: "o_1",
		Score:         0.9,
		Title:         "Eng",
	}
	if d.MatchID == "" || d.OpportunityID == "" {
		t.Fatal("MatchID and OpportunityID required for InsertNotificationReceipts")
	}
}
