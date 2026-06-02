package domain_test

import (
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestRawPayload_StatusDefault(t *testing.T) {
	r := &domain.RawPayload{}
	if r.Status != "" {
		t.Fatalf("zero value Status = %q; want empty (DB default applies)", r.Status)
	}
}

func TestRawPayload_AttemptedTracking(t *testing.T) {
	now := time.Now().UTC()
	r := &domain.RawPayload{
		Status:       domain.RawPayloadStatusFailed,
		AttemptCount: 3,
		NextRetryAt:  &now,
		LastError:    "extractor: timeout",
	}
	if r.Status != domain.RawPayloadStatusFailed {
		t.Fatalf("Status = %q; want %q", r.Status, domain.RawPayloadStatusFailed)
	}
	if r.AttemptCount != 3 {
		t.Fatalf("AttemptCount = %d; want 3", r.AttemptCount)
	}
}
