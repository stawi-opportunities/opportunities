package services

import (
	"context"
	"fmt"
)

// CreateTrackedLink creates a short tracked link via the redirect service.
// If the client is nil, the raw destinationURL is returned as a graceful fallback.
// candidateID is used as an opaque affiliate identifier — never PII.
func CreateTrackedLink(
	ctx context.Context,
	client *RedirectClient,
	destinationURL string,
	candidateID string,
	campaign string,
) (string, error) {
	if client == nil {
		return destinationURL, nil
	}

	result, err := client.CreateLink(ctx, &RedirectLink{
		DestinationURL: destinationURL,
		AffiliateID:    "candidate_" + candidateID,
		Campaign:       campaign,
		Source:         "opportunities",
		Medium:         "platform",
	})
	if err != nil {
		return destinationURL, fmt.Errorf("redirect CreateLink: %w", err)
	}

	if result.Slug != "" {
		return fmt.Sprintf("/r/%s", result.Slug), nil
	}

	return destinationURL, nil
}
