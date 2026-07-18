package placement

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildConversationDigest_UserTurnsOnly(t *testing.T) {
	t.Parallel()
	digest := BuildConversationDigest([]ChatTurn{
		{Role: "assistant", Content: "What role are you targeting?"},
		{Role: "user", Content: "I want senior backend roles in Kenya with Go and PostgreSQL"},
		{Role: "user", Content: "ok"},
		{Role: "user", Content: "Salary around USD 80k and remote is fine"},
	}, 1200)
	require.Contains(t, digest, "backend")
	require.Contains(t, digest, "Kenya")
	require.NotContains(t, strings.ToLower(digest), "what role are you")
	require.NotContains(t, digest, "\nok\n")
}

func TestBuildPersonaDocument_IncludesIntent(t *testing.T) {
	t.Parallel()
	doc := BuildPersonaDocument("c1", Fields{
		TargetJobTitle:     "Backend Engineer",
		ExperienceLevel:    "senior",
		JobTypes:           []string{"Full-time"},
		PreferredCountries: []string{"KE"},
		ExtraInfo:          strings.Repeat("CURRICULUM VITAE\nSoftware Engineer with Go experience. ", 20),
	}, []ChatTurn{
		{Role: "user", Content: "Focus on distributed systems and fintech platforms"},
	}, DefaultPersonaConfig())
	require.Contains(t, doc.SummaryText, "Matching persona v1")
	require.Contains(t, doc.SummaryText, "Intent (conversation-grounded)")
	require.Contains(t, doc.SummaryText, "distributed systems")
	require.NotEmpty(t, doc.ConversationDigest)
	require.NotEmpty(t, doc.RerankText)
	require.NotEmpty(t, doc.ContentHash)
	require.Contains(t, doc.RerankText, "Backend Engineer")
}

func TestContentHash_Stable(t *testing.T) {
	t.Parallel()
	a := ContentHash("hello persona")
	b := ContentHash("hello persona")
	c := ContentHash("hello persona!")
	require.Equal(t, a, b)
	require.NotEqual(t, a, c)
}
