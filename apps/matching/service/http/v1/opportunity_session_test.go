package v1

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatOpportunityListingDoc(t *testing.T) {
	t.Parallel()
	doc := formatOpportunityListingDoc(&opportunityChatContext{
		ID: "opp1", Slug: "pm-sa", Title: "Project Manager",
		Entity: "HandPicked", Location: "SA", Kind: "job",
		ApplyURL: "https://example.com/a", Description: "Lead IT rollouts.",
	})
	require.Contains(t, doc, "Title: Project Manager")
	require.Contains(t, doc, "Company/entity: HandPicked")
	require.Contains(t, doc, "Location: SA")
	require.Contains(t, doc, "Apply URL:")
	require.Contains(t, doc, "Lead IT rollouts")
}

func TestOpportunityCardFromContext(t *testing.T) {
	t.Parallel()
	c := opportunityCardFromContext(&opportunityChatContext{
		Title: "PM", Entity: "Acme", Location: "KE", Slug: "pm-ke", Kind: "job",
		ApplyURL: "https://x/apply",
	})
	require.NotNil(t, c)
	require.Equal(t, "PM", c.Title)
	require.True(t, strings.Contains(c.Subtitle, "Acme"))
	require.Equal(t, "/jobs/pm-ke/", c.Href)
	require.Equal(t, "https://x/apply", c.ApplyURL)
}
