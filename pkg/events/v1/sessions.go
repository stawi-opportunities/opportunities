package eventsv1

import "time"

// SessionCapturedV1 is emitted by the matching service when the browser
// extension uploads a fresh session for a (candidate, source) pair.
// Analytics-only: the writer flattens this for Iceberg so we can
// dashboard "captures per source per day".
//
// The payload contains no cookie material — those live encrypted in
// candidate_sessions. The only secret-adjacent field is UserAgent,
// which is a Privacy: 1 datum (kept for replay-time fingerprint
// matching, not exposed in any read API).
type SessionCapturedV1 struct {
	CandidateID   string    `json:"candidate_id"`
	SourceType    string    `json:"source_type"`
	CapturedAt    time.Time `json:"captured_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	UserAgent     string    `json:"user_agent,omitempty"`
	CaptureOrigin string    `json:"capture_origin"`
}

// SessionRequiredV1 is emitted by the autoapply handler when an intent
// arrives but no live session exists for the (candidate, source). The
// notification service consumes this to surface a "Reconnect your
// account" CTA in the candidate's dashboard.
type SessionRequiredV1 struct {
	CandidateID    string    `json:"candidate_id"`
	SourceType     string    `json:"source_type"`
	CanonicalJobID string    `json:"canonical_job_id,omitempty"`
	ApplyURL       string    `json:"apply_url,omitempty"`
	RequestedAt    time.Time `json:"requested_at"`
}

// SessionExpiredV1 is emitted by the autoapply replay leg when it
// detects a captured session is no longer accepted by the source
// (redirect to login URL, 401, captcha wall, manifest-declared
// logged_out_marker substring match).
//
// Reason is a short, low-cardinality string ("redirect_to_login",
// "http_401", "captcha_wall", "logged_out_marker") for metric
// labelling.
type SessionExpiredV1 struct {
	CandidateID string    `json:"candidate_id"`
	SourceType  string    `json:"source_type"`
	Reason      string    `json:"reason"`
	DetectedAt  time.Time `json:"detected_at"`
}
