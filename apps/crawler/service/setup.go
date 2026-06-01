package service

import (
	"context"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/arbeitnow"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/greenhouse"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/himalayas"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/jobicy"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/remoteok"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/sitemapcrawler"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/smartrecruiters"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	// Blank imports register each spec-driven impl into spec's
	// internal type→impl table via init(). NewFromYAML then resolves
	// every uploaded YAML to the right Impl without the registry call
	// site knowing about per-type packages.
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/htmllisting"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/jsonfeed"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/rssfeed"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/sitemap"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/xmlfeed"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/themuse"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/universal"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/workday"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// BuildRegistry creates a connector Registry with all available connectors
// registered: the hand-coded ones (greenhouse, workday, etc.) plus any
// declarative spec-driven connectors loaded from R2 via the
// definitions service.
//
// When extractor is non-nil the universal AI connector is registered
// for every HTML-based source type; otherwise those types are skipped
// (they cannot work without AI link discovery).
//
// When loader is nil (dev/OSS path without R2) the spec-driven
// connectors are skipped — only the hand-coded ones are registered.
func BuildRegistry(ctx context.Context, client *httpx.Client, extractor *extraction.Extractor, loader *definitions.R2Loader) *connectors.Registry {
	reg := connectors.NewRegistry()

	// Free JSON API connectors (no httpx.Client dependency).
	reg.Register(remoteok.New())
	reg.Register(arbeitnow.New())
	reg.Register(jobicy.New())
	reg.Register(themuse.New())
	reg.Register(himalayas.New())

	// ATS / structured-data connectors (require httpx.Client).
	reg.Register(greenhouse.New(client))
	reg.Register(workday.New(client))
	reg.Register(smartrecruiters.New(client))

	// Sitemap crawler — discovers job URLs from robots.txt sitemaps.
	reg.Register(sitemapcrawler.New(client))

	// Universal AI connector for all HTML-based source types.
	if extractor != nil {
		for _, st := range []domain.SourceType{
			domain.SourceBrighterMonday,
			domain.SourceJobberman,
			domain.SourceMyJobMag,
			domain.SourceNjorku,
			domain.SourceCareers24,
			domain.SourcePNet,
			domain.SourceSchemaOrg,
			domain.SourceHostedBoards,
			domain.SourceGenericHTML,
			domain.SourceSmartRecruitersPage,
		} {
			reg.Register(universal.NewTyped(client, extractor, st))
		}
	}

	// Declarative spec-driven connectors live in R2 under
	// definitions/connector/. Walk the active set at boot, then keep
	// the registry live by subscribing to type=connector changes —
	// new uploads (and re-uploads with edits) propagate within
	// seconds of the broadcast event.
	if loader != nil {
		registerSpecConnectors(ctx, reg, loader, client)
		loader.Subscribe(definitions.TypeConnector, func(name, _ string) {
			util.Log(ctx).WithField("name", name).Info("connectors: spec changed, refreshing registry")
			registerSpecConnectors(ctx, reg, loader, client)
		})
	}

	return reg
}

// registerSpecConnectors lists every active spec under
// definitions/connector/, parses each one, and (re-)registers it on
// the supplied connector registry. Registry.Register overwrites on
// duplicate key, so calling this from the loader subscriber rebuilds
// in place — no stop-the-world swap needed.
func registerSpecConnectors(ctx context.Context, reg *connectors.Registry, loader *definitions.R2Loader, client *httpx.Client) {
	entries, err := loader.List(ctx, definitions.TypeConnector)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("connectors: list spec connectors failed")
		return
	}
	registered := 0
	for _, e := range entries {
		body, _, gerr := loader.Get(ctx, definitions.TypeConnector, e.Name)
		if gerr != nil {
			util.Log(ctx).WithError(gerr).WithField("name", e.Name).Warn("connectors: get spec failed; skipping")
			continue
		}
		c, perr := spec.NewFromYAML(e.Name, body, client)
		if perr != nil {
			util.Log(ctx).WithError(perr).WithField("name", e.Name).Warn("connectors: spec invalid; skipping")
			continue
		}
		reg.Register(c)
		registered++
	}
	if registered > 0 {
		util.Log(ctx).WithField("count", registered).Info("connectors: spec-driven connectors registered")
	}
}
