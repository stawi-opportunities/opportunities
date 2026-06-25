package authsession_test

import (
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/authsession"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func ptr[T any](v T) *T { return &v }

func TestIsRowExpired(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		row  *domain.CandidateSession
		want bool
	}{
		{"nil row is expired", nil, true},
		{"no expiry, no revoke → live", &domain.CandidateSession{}, false},
		{"revoked → expired", &domain.CandidateSession{RevokedAt: ptr(now.Add(-time.Hour))}, true},
		{"expiry in past → expired", &domain.CandidateSession{ExpiresAt: ptr(now.Add(-time.Second))}, true},
		{"expiry now → expired", &domain.CandidateSession{ExpiresAt: ptr(now)}, true},
		{"expiry in future → live", &domain.CandidateSession{ExpiresAt: ptr(now.Add(time.Second))}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := authsession.IsRowExpired(tc.row, now); got != tc.want {
				t.Fatalf("IsRowExpired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInferExpiry(t *testing.T) {
	captured := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	t.Run("picks earliest future cookie expiry", func(t *testing.T) {
		payload := domain.SessionPayload{
			Cookies: []domain.SessionCookie{
				{Name: "old", Expires: ptr(captured.Add(-time.Hour))}, // past — ignored
				{Name: "long", Expires: ptr(captured.Add(72 * time.Hour))},
				{Name: "short", Expires: ptr(captured.Add(12 * time.Hour))},
				{Name: "session", Expires: nil}, // session cookie — ignored
			},
		}
		got := authsession.InferExpiry(payload, captured, 30*24*time.Hour)
		if got == nil {
			t.Fatal("nil expiry")
		}
		if !got.Equal(captured.Add(12 * time.Hour)) {
			t.Fatalf("got %v, want %v", got, captured.Add(12*time.Hour))
		}
	})

	t.Run("falls back to TTL when no cookie expires in future", func(t *testing.T) {
		payload := domain.SessionPayload{
			Cookies: []domain.SessionCookie{{Name: "session", Expires: nil}},
		}
		got := authsession.InferExpiry(payload, captured, 30*24*time.Hour)
		if got == nil {
			t.Fatal("nil expiry")
		}
		if !got.Equal(captured.Add(30 * 24 * time.Hour)) {
			t.Fatalf("got %v, want fallback", got)
		}
	})

	t.Run("returns nil when no cookie expiry and zero TTL", func(t *testing.T) {
		got := authsession.InferExpiry(domain.SessionPayload{}, captured, 0)
		if got != nil {
			t.Fatalf("want nil, got %v", got)
		}
	})

	t.Run("ignores expired cookies", func(t *testing.T) {
		payload := domain.SessionPayload{
			Cookies: []domain.SessionCookie{
				{Name: "old1", Expires: ptr(captured.Add(-time.Hour))},
				{Name: "old2", Expires: ptr(captured.Add(-2 * time.Hour))},
			},
		}
		got := authsession.InferExpiry(payload, captured, 24*time.Hour)
		if got == nil || !got.Equal(captured.Add(24*time.Hour)) {
			t.Fatalf("want fallback, got %v", got)
		}
	})
}
