package services

import (
	"context"
	"fmt"

	redirectpb "github.com/antinvestor/service-files/apps/redirect/gen/redirect/v1"
	"github.com/antinvestor/service-files/apps/redirect/gen/redirect/v1/redirectv1connect"
	"connectrpc.com/connect"
)

// CreateTrackedLink creates a short tracked link via the redirect service.
// If the client is nil, the raw destinationURL is returned as a graceful fallback.
// affiliateID should be an opaque identifier like "candidate_123" — never PII.
func CreateTrackedLink(
	ctx context.Context,
	client redirectv1connect.RedirectServiceClient,
	destinationURL string,
	candidateID int64,
	campaign string,
) (string, error) {
	if client == nil {
		return destinationURL, nil
	}

	resp, err := client.CreateLink(ctx, connect.NewRequest(&redirectpb.CreateLinkRequest{
		Data: &redirectpb.Link{
			DestinationUrl: destinationURL,
			AffiliateId:    fmt.Sprintf("candidate_%d", candidateID),
			Campaign:       campaign,
			Source:         "stawi-jobs",
			Medium:         "platform",
		},
	}))
	if err != nil {
		return destinationURL, fmt.Errorf("redirect CreateLink: %w", err)
	}

	if resp.Msg.GetData().GetSlug() != "" {
		return fmt.Sprintf("/r/%s", resp.Msg.GetData().GetSlug()), nil
	}

	return destinationURL, nil
}
