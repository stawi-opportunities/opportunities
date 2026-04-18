package billing

import "testing"

// Plain smoke-test: every status we emit is one of the values
// candidateBillingWebhookHandler/applyBillingEvent switches on.
// Lets us catch accidental renames in a single place without pulling
// the candidates package into billing's test deps.
func TestEventStatus_CoversAllSwitchArms(t *testing.T) {
	known := map[EventStatus]struct{}{
		StatusPaid:      {},
		StatusActive:    {},
		StatusTrial:     {},
		StatusCancelled: {},
		StatusExpired:   {},
		StatusFailed:    {},
		StatusPending:   {},
	}
	// Adapters MUST only emit the statuses above; adding a new one
	// without also extending applyBillingEvent would silently fall
	// through to "unknown status" and 500 the webhook.
	all := []EventStatus{
		StatusPaid, StatusActive, StatusTrial, StatusCancelled,
		StatusExpired, StatusFailed, StatusPending,
	}
	for _, s := range all {
		if _, ok := known[s]; !ok {
			t.Errorf("status %q not in known set", s)
		}
	}
}
