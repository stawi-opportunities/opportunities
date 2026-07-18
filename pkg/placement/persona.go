package placement

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// ChatTurn is a single user/assistant message for conversation digesting.
// Kept independent of HTTP DTOs so matching can build digests without imports.
type ChatTurn struct {
	Role    string // "user" | "assistant"
	Content string
}

// PersonaConfig tunes section budgets for embedding (runes, not bytes).
type PersonaConfig struct {
	// MaxRunes is the total embed budget for the persona document.
	MaxRunes int
	// DigestMaxRunes caps the conversation intent section.
	DigestMaxRunes int
	// IncludeConversation when false skips the intent section.
	IncludeConversation bool
}

// DefaultPersonaConfig is production-safe for e5-class models.
func DefaultPersonaConfig() PersonaConfig {
	return PersonaConfig{
		MaxRunes:            3500,
		DigestMaxRunes:      1200,
		IncludeConversation: true,
	}
}

// BuildConversationDigest extracts a compact, high-signal intent blob from chat.
// Prefer user turns; keep last N and filter filler so Path A stays semantic.
func BuildConversationDigest(turns []ChatTurn, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 1200
	}
	var userLines []string
	for _, t := range turns {
		if !strings.EqualFold(strings.TrimSpace(t.Role), "user") {
			continue
		}
		line := compressChatLine(t.Content)
		if line == "" {
			continue
		}
		userLines = append(userLines, line)
	}
	if len(userLines) == 0 {
		return ""
	}
	// Keep the most recent high-signal turns (tail).
	const keep = 12
	if len(userLines) > keep {
		userLines = userLines[len(userLines)-keep:]
	}
	body := strings.Join(userLines, "\n")
	return strings.TrimSpace(truncateRunes(body, maxRunes))
}

func compressChatLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Drop pure short acknowledgements.
	lower := strings.ToLower(s)
	switch lower {
	case "ok", "okay", "yes", "no", "thanks", "thank you", "hi", "hello", "hey":
		return ""
	}
	// Strip opportunity-view chrome if present.
	if strings.HasPrefix(s, "[Viewing opportunity:") {
		if i := strings.Index(s, "]"); i >= 0 && i+1 < len(s) {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	// Collapse whitespace.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len([]rune(out)) < 8 {
		return ""
	}
	return truncateRunes(out, 400)
}

// ContentHash is a stable fingerprint of the embeddable persona text.
func ContentHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:16])
}

// RerankText builds a short document for Path A cross-encoder stage-2.
func RerankText(headline, prefs, digest string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 1800
	}
	var parts []string
	if h := strings.TrimSpace(headline); h != "" {
		parts = append(parts, h)
	}
	if d := strings.TrimSpace(digest); d != "" {
		parts = append(parts, "Intent:\n"+truncateRunes(d, 600))
	}
	if p := strings.TrimSpace(prefs); p != "" {
		// Drop markdown header noise for denser signal.
		p = strings.TrimPrefix(p, "## Preferences (what they want)")
		parts = append(parts, strings.TrimSpace(p))
	}
	return strings.TrimSpace(truncateRunes(strings.Join(parts, "\n\n"), maxRunes))
}
