package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/telemetry"
)

var sourceExpandTracer = otel.Tracer("stawi.jobs.pipeline")

// blockedDomains lists domains that are not job boards and should be skipped
// during source expansion. The stawi.org entries are the critical ones —
// without them, URL discovery can pick up links from our own published job
// pages / redirect hops / API JSON and round-trip our content back through
// the pipeline, creating a cycle. Anything on *.stawi.org is ours.
var blockedDomains = map[string]bool{
	"google.com":       true,
	"facebook.com":     true,
	"twitter.com":      true,
	"instagram.com":    true,
	"youtube.com":      true,
	"wikipedia.org":    true,
	"linkedin.com":     true,
	"github.com":       true,
	"apple.com":        true,
	"play.google.com":  true,
	"apps.apple.com":   true,

	// Our own infrastructure — anti-loop protection.
	"stawi.org":        true, // root + *.stawi.org via the suffix check
	"stawi.jobs":       true, // legacy domain, just in case
	"pages.dev":        true, // CF Pages branch previews
}

// SourceExpansionHandler processes source.urls.discovered events and upserts
// newly discovered job source URLs as active sources for future crawling.
type SourceExpansionHandler struct {
	sourceRepo *repository.SourceRepository
	httpClient *http.Client
}

// NewSourceExpansionHandler creates a SourceExpansionHandler with a redirect-
// following HTTP client and the given source repository.
func NewSourceExpansionHandler(sourceRepo *repository.SourceRepository) *SourceExpansionHandler {
	return &SourceExpansionHandler{
		sourceRepo: sourceRepo,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
}

// Name returns the event name this handler processes.
func (h *SourceExpansionHandler) Name() string {
	return EventSourceURLsDiscovered
}

// PayloadType returns a zero-value pointer for JSON deserialization.
func (h *SourceExpansionHandler) PayloadType() any {
	return &SourceURLsPayload{}
}

// Validate checks the payload before execution.
func (h *SourceExpansionHandler) Validate(_ context.Context, payload any) error {
	p, ok := payload.(*SourceURLsPayload)
	if !ok {
		return errors.New("invalid payload type, expected *SourceURLsPayload")
	}
	if p.SourceID == 0 {
		return errors.New("source_id is required")
	}
	return nil
}

// Execute processes each discovered URL: follows redirects, extracts base domain,
// filters blocked or same-domain URLs, then upserts new sources.
func (h *SourceExpansionHandler) Execute(ctx context.Context, payload any) error {
	p, ok := payload.(*SourceURLsPayload)
	if !ok {
		return errors.New("invalid payload type")
	}

	ctx, span := sourceExpandTracer.Start(ctx, "pipeline.source_expand")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("source_id", p.SourceID),
		attribute.Int("url_count", len(p.URLs)),
	)

	start := time.Now()
	defer func() {
		if telemetry.StageDuration != nil {
			telemetry.StageDuration.Record(ctx, time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("stage", "source_expand")),
			)
		}
	}()

	// Load the source to know its own domain so we can skip self-references.
	src, err := h.sourceRepo.GetByID(ctx, p.SourceID)
	if err != nil {
		return fmt.Errorf("source_expand: load source %d: %w", p.SourceID, err)
	}

	sourceDomain := extractHostname(src.BaseURL)

	newCount := 0
	for _, rawURL := range p.URLs {
		resolvedBase, err := h.resolveBaseURL(ctx, rawURL)
		if err != nil {
			log.Printf("source_expand: resolve %q: %v", rawURL, err)
			continue
		}

		host := extractHostname(resolvedBase)
		if host == "" {
			continue
		}

		// Skip blocked domains.
		if isBlockedDomain(host) {
			continue
		}

		// Skip URLs that are on the same domain as the originating source.
		if host == sourceDomain {
			continue
		}

		// Upsert — ON CONFLICT (type, base_url) is handled by the repository,
		// so duplicates are safe.
		newSrc := &domain.Source{
			Type:             domain.SourceGenericHTML,
			BaseURL:          resolvedBase,
			Status:           domain.SourceActive,
			Priority:         domain.PriorityNormal,
			CrawlIntervalSec: 7200,
			HealthScore:      1.0,
			Config:           "{}",
		}
		if err := h.sourceRepo.Upsert(ctx, newSrc); err != nil {
			log.Printf("source_expand: upsert %q: %v", resolvedBase, err)
			continue
		}
		newCount++
	}

	log.Printf("source_expand: discovered %d new sources from source %d", newCount, p.SourceID)
	return nil
}

// resolveBaseURL follows redirects for the given rawURL (using a GET request so
// the http.Client's CheckRedirect kicks in) and returns the scheme+host of the
// final destination.
func (h *SourceExpansionHandler) resolveBaseURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; StawiBot/1.0)")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		// Even on error the request URL reflects the last hop followed.
		if req.URL != nil {
			return schemeHost(req.URL), nil
		}
		return "", err
	}
	_ = resp.Body.Close()

	// resp.Request.URL is the final URL after all redirects.
	return schemeHost(resp.Request.URL), nil
}

// schemeHost returns "scheme://host" from a parsed URL.
func schemeHost(u *url.URL) string {
	if u == nil {
		return ""
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}

// extractHostname returns the lowercased hostname from a base URL string
// (scheme://host), stripping a leading "www." prefix.
func extractHostname(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	return strings.TrimPrefix(host, "www.")
}

// isBlockedDomain returns true if host (without www.) is in the blocked list.
func isBlockedDomain(host string) bool {
	// Check exact match and parent domain match (e.g. "play.google.com").
	if blockedDomains[host] {
		return true
	}
	// Also check if the host ends with a blocked domain (subdomain guard).
	for blocked := range blockedDomains {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}
