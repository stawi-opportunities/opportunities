package definitions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// NewR2LoaderFromEnv constructs an R2Loader using the standard R2 env
// vars (R2_ACCOUNT_ID / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY /
// R2_CONTENT_BUCKET) that every service already has via its config
// struct.
//
// Returns (nil, nil) when R2_ACCOUNT_ID or R2_CONTENT_BUCKET is empty —
// graceful degradation for OSS / dev deploys where R2 isn't configured
// and the caller should fall back to opportunity.LoadFromDir.
func NewR2LoaderFromEnv(_ context.Context) (*R2Loader, error) {
	accountID := os.Getenv("R2_ACCOUNT_ID")
	bucket := os.Getenv("R2_CONTENT_BUCKET")
	if accountID == "" || bucket == "" {
		return nil, nil
	}
	accessKey := os.Getenv("R2_ACCESS_KEY_ID")
	secretKey := os.Getenv("R2_SECRET_ACCESS_KEY")
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("definitions: R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY must be set when R2_ACCOUNT_ID is set")
	}
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  awscreds.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		BaseEndpoint: aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)),
	})
	return NewR2Loader(R2Config{
		Client: client,
		Bucket: bucket,
		Prefix: "definitions",
	}), nil
}

// KindRebuildFn is the per-app callback invoked by BroadcastConsumer
// when a kind definition changes. Implementations typically call
// opportunity.LoadFromDefinitions(ctx, loader) and Registry.Replace to
// live-swap the registry without a process restart.
//
// Defined as a function rather than an opportunity.Registry handle so
// pkg/definitions doesn't import pkg/opportunity (circular).
type KindRebuildFn func(ctx context.Context) error

// BroadcastConsumer is the Frame event handler for
// opportunities.definitions.changed.v1. On every event it invalidates
// the loader's cache for the (type, name) pair so the next read serves
// fresh content (vs. waiting up to 5 min for the refresh tick). For
// kind events it additionally invokes the rebuild callback so each
// replica live-rebuilds its opportunity.Registry.
type BroadcastConsumer struct {
	loader  *R2Loader
	rebuild KindRebuildFn // optional; nil disables registry live-swap
}

// NewBroadcastConsumer wires a consumer. rebuild may be nil for apps
// that don't carry an opportunity.Registry (none today, but kept open
// for sidecars).
func NewBroadcastConsumer(loader *R2Loader, rebuild KindRebuildFn) *BroadcastConsumer {
	return &BroadcastConsumer{loader: loader, rebuild: rebuild}
}

// Name implements frame.EventI. Matches the broadcast topic so Frame
// routes events to this handler.
func (c *BroadcastConsumer) Name() string { return eventsv1.TopicDefinitionsChanged }

// PayloadType returns a raw-message holder so Execute can decode the
// envelope itself (Frame's typed dispatch isn't a good fit for the
// generic Envelope[P] wrapper).
func (c *BroadcastConsumer) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ensures the payload is non-empty. The full schema check
// happens in Execute.
func (c *BroadcastConsumer) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("definitions broadcast: empty payload")
	}
	return nil
}

// Execute invalidates the loader cache and, for kind changes, fires the
// rebuild callback. Errors in the rebuild are logged + swallowed so a
// transient R2 hiccup doesn't push the message back onto the queue and
// retry-storm every replica.
func (c *BroadcastConsumer) Execute(ctx context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil {
		return errors.New("definitions broadcast: wrong payload type")
	}
	var env eventsv1.Envelope[eventsv1.DefinitionsChangedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("definitions broadcast: decode: %w", err)
	}
	p := env.Payload
	log := util.Log(ctx).
		WithField("type", p.Type).
		WithField("name", p.Name).
		WithField("action", p.Action)

	// Wildcard reload — clear nothing here; the 5-min refresh tick will
	// pick up everything. Aggressive eviction would require touching
	// every cache key, and the reload path is rare enough that a few
	// minutes of staleness is acceptable.
	if p.Name == "" || p.Name == "*" || p.Type == "*" {
		log.Info("definitions broadcast: wildcard reload received; relying on refresh tick")
		return nil
	}

	if c.loader != nil {
		if err := c.loader.Invalidate(ctx, Type(p.Type), p.Name); err != nil {
			// Invalidate returns nil even on 404 (treats as delete);
			// any error here is a fetch failure on R2 — log + drop.
			log.WithError(err).Warn("definitions broadcast: invalidate failed; cache stays stale until next refresh")
			return nil
		}
	}

	// Rebuild the in-memory opportunity.Registry for kind events. The
	// callback handles the LoadFromDefinitions + Replace pair so the
	// definitions package stays free of opportunity imports.
	if c.rebuild != nil && Type(p.Type) == TypeKind {
		if err := c.rebuild(ctx); err != nil {
			log.WithError(err).Warn("definitions broadcast: registry rebuild failed; keeping stale registry")
			return nil
		}
		log.Info("definitions broadcast: registry rebuilt")
	}
	return nil
}
