package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/domain"
	eventsv1 "stawi.jobs/pkg/events/v1"
)

// SourceUpserter is the narrow slice of SourceRepository used by the
// source-discovered handler.
type SourceUpserter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
	Upsert(ctx context.Context, src *domain.Source) error
}

// blockedDiscoveredDomains mirrors the blocklist in the legacy
// pkg/pipeline/handlers/source_expand.go. Kept local so this file can
// be tested without importing the legacy handler, and so the legacy
// file can be deleted in Phase 6 without touching Plan 4 code.
var blockedDiscoveredDomains = map[string]bool{
	"google.com":      true,
	"facebook.com":    true,
	"twitter.com":     true,
	"instagram.com":   true,
	"youtube.com":     true,
	"wikipedia.org":   true,
	"linkedin.com":    true,
	"github.com":      true,
	"apple.com":       true,
	"play.google.com": true,
	"apps.apple.com":  true,
	"stawi.org":       true,
	"stawi.jobs":      true,
	"pages.dev":       true,
}

// SourceDiscoveredHandler consumes sources.discovered.v1 and upserts
// the target URL as a `generic-html` source. Same blocklist + self-
// domain filter as the legacy SourceExpansionHandler; payload shape
// changed from SourceURLsPayload (batched) to one event per URL.
type SourceDiscoveredHandler struct {
	repo SourceUpserter
}

// NewSourceDiscoveredHandler wires the handler.
func NewSourceDiscoveredHandler(repo SourceUpserter) *SourceDiscoveredHandler {
	return &SourceDiscoveredHandler{repo: repo}
}

// Name implements frame.EventI.
func (h *SourceDiscoveredHandler) Name() string { return eventsv1.TopicSourcesDiscovered }

// PayloadType yields a raw message for typed decode inside Execute.
func (h *SourceDiscoveredHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures we have a parseable envelope.
func (h *SourceDiscoveredHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("source-discovered: empty payload")
	}
	return nil
}

// Execute upserts the discovered URL as a new source, unless the URL
// resolves to the same domain as the origin source or is on the
// block list.
func (h *SourceDiscoveredHandler) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("source-discovered: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.SourceDiscoveredV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("source-discovered: decode: %w", err)
	}
	p := env.Payload

	log := util.Log(ctx).WithField("origin_id", p.SourceID).WithField("url", p.DiscoveredURL)

	origin, err := h.repo.GetByID(ctx, p.SourceID)
	if err != nil {
		return fmt.Errorf("source-discovered: GetByID: %w", err)
	}
	if origin == nil {
		log.Warn("source-discovered: origin source missing; dropping")
		return nil
	}

	target, err := url.Parse(p.DiscoveredURL)
	if err != nil || target.Host == "" {
		log.Debug("source-discovered: unparseable URL")
		return nil
	}
	host := strings.ToLower(strings.TrimPrefix(target.Hostname(), "www."))

	originHost := ""
	if ou, perr := url.Parse(origin.BaseURL); perr == nil {
		originHost = strings.ToLower(strings.TrimPrefix(ou.Hostname(), "www."))
	}
	if host == originHost {
		return nil
	}

	if isBlockedHost(host) {
		return nil
	}

	baseURL := fmt.Sprintf("%s://%s", target.Scheme, target.Host)
	newSrc := &domain.Source{
		Type:             domain.SourceGenericHTML,
		BaseURL:          baseURL,
		Country:          p.Country,
		Status:           domain.SourceActive,
		Priority:         domain.PriorityNormal,
		CrawlIntervalSec: 7200,
		HealthScore:      1.0,
		Config:           "{}",
	}
	if err := h.repo.Upsert(ctx, newSrc); err != nil {
		log.WithError(err).Warn("source-discovered: upsert failed")
		return nil
	}
	log.Info("source-discovered: upserted new source")
	return nil
}

func isBlockedHost(host string) bool {
	if blockedDiscoveredDomains[host] {
		return true
	}
	for blocked := range blockedDiscoveredDomains {
		if strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}
