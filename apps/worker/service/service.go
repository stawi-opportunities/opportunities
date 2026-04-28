package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/cache"
	"github.com/pitabwire/frame/events"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/publish"
)

// Service is the worker's composition root. Handlers returns the
// seven internal subscriptions to be registered with the Frame event
// manager via frame.WithRegisterEvents.
//
// TODO(golang-patterns): the embed/translate handlers call external
// LLM endpoints; per the canonical Frame decision tree these are
// "external API" work and should be Queue subscribers rather than
// Events. Migrate when the pipeline's chained-fanout semantics can be
// expressed as durable pub/sub — today the canonicalFanout
// composition relies on event ordering that Queue retries would
// disrupt without coordinated dedupe.
type Service struct {
	svc       *frame.Service
	extractor *extraction.Extractor
	publisher *publish.R2Publisher
	registry  *opportunity.Registry

	dedupCache   cache.Cache[string, string]
	clusterCache cache.Cache[string, kv.ClusterSnapshot]

	translationLangs []string
}

// NewService ...
func NewService(
	svc *frame.Service,
	ex *extraction.Extractor,
	publisher *publish.R2Publisher,
	registry *opportunity.Registry,
	dedupCache cache.Cache[string, string],
	clusterCache cache.Cache[string, kv.ClusterSnapshot],
	translationLangs []string,
) *Service {
	return &Service{
		svc:              svc,
		extractor:        ex,
		publisher:        publisher,
		registry:         registry,
		dedupCache:       dedupCache,
		clusterCache:     clusterCache,
		translationLangs: translationLangs,
	}
}

// Handlers returns the four registered Frame event handlers.
//
// Frame's event registry maps one handler per topic name. Three
// downstream stages (embed, translate, publish) all react to
// TopicCanonicalsUpserted, so they are wrapped in a single
// canonicalFanout handler that dispatches to each in turn.
func (s *Service) Handlers() []events.EventI {
	return []events.EventI{
		NewNormalizeHandler(s.svc),
		NewValidateHandler(s.svc, s.extractor),
		NewDedupHandlerWithCluster(s.svc, s.dedupCache, s.clusterCache),
		NewCanonicalHandler(s.svc, s.clusterCache),
		newCanonicalFanout(s.svc, s.extractor, s.publisher, s.registry, s.translationLangs),
	}
}

// canonicalFanout is a single Frame handler registered on
// TopicCanonicalsUpserted that sequentially calls the embed,
// translate, and publish sub-handlers. This satisfies Frame's
// one-handler-per-topic constraint while preserving the three logical
// stages.
type canonicalFanout struct {
	embed     *EmbedHandler
	translate *TranslateHandler
	publish   *PublishHandler
}

func newCanonicalFanout(
	svc *frame.Service,
	ex *extraction.Extractor,
	pub *publish.R2Publisher,
	reg *opportunity.Registry,
	langs []string,
) *canonicalFanout {
	return &canonicalFanout{
		embed:     NewEmbedHandler(svc, ex),
		translate: NewTranslateHandler(svc, ex, langs),
		publish:   NewPublishHandler(svc, pub, reg),
	}
}

// Name registers this handler on the canonical-upsert topic.
func (f *canonicalFanout) Name() string { return eventsv1.TopicCanonicalsUpserted }

// PayloadType returns the raw JSON template used by Frame's dispatcher.
func (f *canonicalFanout) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate accepts any non-empty JSON payload.
func (f *canonicalFanout) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("canonical-fanout: empty payload")
	}
	return nil
}

// Execute calls embed, translate, and publish in order. Each
// sub-handler is fail-open on its own AI calls; a hard error (e.g.
// R2 write failure) propagates so Frame can redeliver.
func (f *canonicalFanout) Execute(ctx context.Context, payload any) error {
	if err := f.embed.Execute(ctx, payload); err != nil {
		return err
	}
	if err := f.translate.Execute(ctx, payload); err != nil {
		return err
	}
	return f.publish.Execute(ctx, payload)
}
