package otprendezvous

import (
	"regexp"
	"strings"
)

// codeAnchor is the stable instruction line Greenhouse places immediately
// before the security code. Anchoring on it (rather than scanning the
// whole body) avoids matching unrelated alphanumeric tokens.
const codeAnchor = "copy and paste this code"

// standaloneCodeRE matches a 6–12 char alphanumeric token that sits alone
// on its own line — which is exactly how the code is rendered in the email
// (a blank line, the code, a blank line). This is the most reliable signal
// and works regardless of whether the code happens to contain a digit.
var standaloneCodeRE = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9]{6,12})\s*$`)

// codeToken matches a security-code-shaped run for the inline fallback.
var codeToken = regexp.MustCompile(`\b[A-Za-z0-9]{6,12}\b`)

// ExtractCode pulls the security code from a Greenhouse OTP email body.
// It anchors on the instruction line, then:
//  1. prefers a token that sits alone on its own line (the code's layout), and
//  2. falls back to the first inline token that "looks like a code".
//
// Case is preserved — the code is case-sensitive (e.g. "CMSqZTCA",
// "51iL5qrq"). Returns "" when no code can be confidently located.
func ExtractCode(body string) string {
	lower := strings.ToLower(body)
	i := strings.Index(lower, codeAnchor)
	if i < 0 {
		return ""
	}
	// Scan the original (case-preserved) slice after the anchor.
	after := body[i+len(codeAnchor):]

	if m := standaloneCodeRE.FindStringSubmatch(after); m != nil {
		return m[1]
	}
	for _, tok := range codeToken.FindAllString(after, -1) {
		if looksLikeCode(tok) {
			return tok
		}
	}
	return ""
}

// looksLikeCode distinguishes a security code from an ordinary English word
// of the same length: a code either contains a digit or mixes upper- and
// lower-case letters ("CMSqZTCA"), whereas prose words ("application",
// "security") are single-case. Used only for the inline fallback.
func looksLikeCode(s string) bool {
	var hasDigit, hasUpper, hasLower bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		}
	}
	return hasDigit || (hasUpper && hasLower)
}
