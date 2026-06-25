package authsession

import (
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// IsRowExpired reports whether a stored row should be treated as no
// longer usable. Revoked rows are always expired. A nil ExpiresAt means
// "we have no expiry hint, trust the replay leg to detect" → not expired
// from our side.
func IsRowExpired(s *domain.CandidateSession, now time.Time) bool {
	if s == nil {
		return true
	}
	if s.RevokedAt != nil {
		return true
	}
	if s.ExpiresAt == nil {
		return false
	}
	return !now.Before(*s.ExpiresAt)
}

// InferExpiry picks an ExpiresAt for a capture. The order of precedence
// matches what gives the strongest signal:
//
//  1. The earliest cookie Expires that is in the future — the actual
//     session almost always dies when its server-set cookie does.
//  2. The fallback duration (e.g. the manifest's session_ttl) added to
//     CapturedAt.
//  3. nil — caller should treat as "rely on replay-time detection".
//
// The first option is the only one that requires looking at the payload,
// which is why this lives in the package and not in the Store.
func InferExpiry(payload domain.SessionPayload, capturedAt time.Time, fallback time.Duration) *time.Time {
	earliest := earliestFutureCookieExpiry(payload.Cookies, capturedAt)
	if earliest != nil {
		return earliest
	}
	if fallback > 0 {
		t := capturedAt.Add(fallback)
		return &t
	}
	return nil
}

func earliestFutureCookieExpiry(cookies []domain.SessionCookie, now time.Time) *time.Time {
	var best *time.Time
	for i := range cookies {
		exp := cookies[i].Expires
		if exp == nil {
			continue
		}
		if !exp.After(now) {
			continue
		}
		if best == nil || exp.Before(*best) {
			t := *exp
			best = &t
		}
	}
	return best
}
