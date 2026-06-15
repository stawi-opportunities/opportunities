package otprendezvous

import (
	"context"
	"testing"
)

// the real Greenhouse OTP email body shape.
const sampleBody = `Greenhouse Recruiting
Hi Joakim,

Copy and paste this code into the security code field on your application:

51iL5qrq

After you enter the code, resubmit your application.`

func TestExtractCode(t *testing.T) {
	if got := ExtractCode(sampleBody); got != "51iL5qrq" {
		t.Fatalf("ExtractCode = %q, want 51iL5qrq", got)
	}
	// Case must be preserved (the code is case-sensitive).
	if got := ExtractCode(sampleBody); got != "51iL5qrq" {
		t.Fatalf("case not preserved: %q", got)
	}
}

func TestExtractCodeAllLetters(t *testing.T) {
	// Real codes can be all letters with no digit (observed: "CMSqZTCA").
	body := "Copy and paste this code into the security code field on your application:\n\nCMSqZTCA\n\nAfter you enter the code, resubmit your application."
	if got := ExtractCode(body); got != "CMSqZTCA" {
		t.Fatalf("ExtractCode = %q, want CMSqZTCA", got)
	}
}

func TestExtractCodeSkipsWords(t *testing.T) {
	// "application" is 11 letters and fits the length window but has no
	// digit, so it must be skipped in favour of the real code.
	body := "Copy and paste this code on your application page: AB12cd99"
	if got := ExtractCode(body); got != "AB12cd99" {
		t.Fatalf("ExtractCode = %q, want AB12cd99", got)
	}
}

func TestExtractCodeNoAnchor(t *testing.T) {
	if got := ExtractCode("totally unrelated email with 12ab34cd inside"); got != "" {
		t.Fatalf("ExtractCode = %q, want empty (no anchor)", got)
	}
}

func TestMarkSeenDedup(t *testing.T) {
	r := NewInMemory()
	ctx := context.Background()

	first, err := r.MarkSeen(ctx, "msg-1@gh")
	if err != nil || !first {
		t.Fatalf("first MarkSeen = (%v,%v), want (true,nil)", first, err)
	}
	again, err := r.MarkSeen(ctx, "msg-1@gh")
	if err != nil || again {
		t.Fatalf("second MarkSeen = (%v,%v), want (false,nil)", again, err)
	}
}
