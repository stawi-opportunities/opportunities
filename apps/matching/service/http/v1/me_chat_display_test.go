package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripViewingChrome(t *testing.T) {
	raw := `[Viewing opportunity: "Time Out South Africa | Multimedia Producer" at Kagiso Media, SA. slug=time-out]

Do I have a valid resume?`
	require.Equal(t, "Do I have a valid resume?", stripViewingChrome(raw))
	require.Equal(t, "plain question", stripViewingChrome("plain question"))
	require.Equal(t, "", stripViewingChrome("[Viewing opportunity: x]"))
}

func TestFilterPlacementMessages_DropsJobChrome(t *testing.T) {
	in := []onboardingChatMessage{
		{Role: "assistant", Content: "You're viewing Time Out South Africa | Multimedia Producer at Kagiso Media (SA). Ask anything."},
		{Role: "user", Content: "[Viewing opportunity: \"X\" at Y. slug=z]\n\nDo I have a valid resume for this job"},
		{Role: "assistant", Content: "What role should we match you to?"},
		{Role: "user", Content: "Senior Software Engineer"},
	}
	out := filterPlacementMessages(in)
	require.Equal(t, []onboardingChatMessage{
		{Role: "assistant", Content: "What role should we match you to?"},
		{Role: "user", Content: "Senior Software Engineer"},
	}, out)
}

func TestSanitizeMessagesForClient_StripsUserChrome(t *testing.T) {
	in := []onboardingChatMessage{
		{Role: "user", Content: "[Viewing opportunity: \"Role\" at Co. slug=r]\n\nAm I a fit?"},
		{Role: "assistant", Content: "Tell me your target title."},
	}
	out := sanitizeMessagesForClient(in)
	require.Equal(t, "Am I a fit?", out[0].Content)
	require.Equal(t, "Tell me your target title.", out[1].Content)
}
