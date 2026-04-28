package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/lib/pq"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/sourceverify"
)

// SourceUpserter is the narrow slice of SourceRepository used by the
// source-discovered handler.
type SourceUpserter interface {
	GetByID(ctx context.Context, id string) (*domain.Source, error)
	Upsert(ctx context.Context, src *domain.Source) error
}

// SourceDiscoveredHandler consumes sources.discovered.v1 and upserts
// the target URL as a `generic-html` source. Same blocklist + self-
// domain filter as the legacy SourceExpansionHandler; payload shape
// changed from SourceURLsPayload (batched) to one event per URL.
type SourceDiscoveredHandler struct {
	repo SourceUpserter
	reg  *opportunity.Registry
}

// NewSourceDiscoveredHandler wires the handler. reg is required so the
// handler can validate the kinds it declares on every newly registered
// source (Phase 4.1 — generic-HTML discoveries default to ["job"], but
// the validator catches a stale registry that no longer recognises that
// kind).
func NewSourceDiscoveredHandler(repo SourceUpserter, reg *opportunity.Registry) *SourceDiscoveredHandler {
	return &SourceDiscoveredHandler{repo: repo, reg: reg}
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
	// Discovered sources are generic-HTML pages whose true kind mix is
	// unknown until the classifier runs over their content. We declare
	// "job" as a conservative default — it covers every connector wired
	// today; once kind YAML for tender/scholarship/etc lands and the
	// classifier is wired, ops can widen this on a per-domain basis.
	kinds := []string{"job"}
	if h.reg != nil {
		for _, k := range kinds {
			if _, ok := h.reg.Lookup(k); !ok {
				return fmt.Errorf("source-discovered: declared kind %q is not in registry (known: %v)",
					k, h.reg.Known())
			}
		}
	}

	// Discovered sources land in SourcePending. They become eligible for
	// crawling only after passing source-level verification AND being
	// approved (manually by an operator, or automatically when
	// AutoApprove=true). The legacy "discovered → active" flow is gone:
	// every new domain now goes through the same lifecycle as an
	// operator-created source, except AutoApprove defaults to false so
	// a real human reviews the verification report before it starts
	// burning crawl budget.
	newSrc := &domain.Source{
		Type:                     domain.SourceGenericHTML,
		BaseURL:                  baseURL,
		Country:                  p.Country,
		Status:                   domain.SourcePending,
		Priority:                 domain.PriorityNormal,
		CrawlIntervalSec:         7200,
		HealthScore:              1.0,
		Config:                   "{}",
		Kinds:                    pq.StringArray(kinds),
		RequiredAttributesByKind: map[string][]string{},
		AutoApprove:              false,
	}
	if err := h.repo.Upsert(ctx, newSrc); err != nil {
		return fmt.Errorf("source-discovered: upsert %s: %w", baseURL, err)
	}
	log.Info("source-discovered: upserted new source")

	// TODO(source-admin): kick off async verification here once the
	// crawler boot wires a sourceverify.Dispatcher. The synchronous
	// admin POST /admin/sources/{id}/verify on apps/api covers the
	// operator-driven path; auto-verify-on-discovery is purely a UX
	// nicety so the operator's first review of a discovered source
	// already shows a populated VerificationReport.
	return nil
}

// isBlockedHost is a thin wrapper around sourceverify.IsBlockedHost so
// existing call sites keep working unchanged.
func isBlockedHost(host string) bool {
	return sourceverify.IsBlockedHost(host)
}
