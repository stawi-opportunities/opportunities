package service

import (
	"context"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/sitemapcrawler"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/smartrecruiters"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/structured"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/workday"
	// Blank imports register each spec-driven impl into spec's
	// internal type→impl table via init().
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/htmllisting"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/jsonfeed"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/rssfeed"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/sitemap"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/xmlfeed"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// BuildRegistry creates a connector Registry of crawl ENGINES only.
// Site-specific boards are data: a source row + extraction recipe (or
// schema.org / sitemap). There are no per-board Go packages.
func BuildRegistry(ctx context.Context, client *httpx.Client, loader *definitions.R2Loader) *connectors.Registry {
	reg := connectors.NewRegistry()

	// Engines — behaviour is parameterized by source.base_url + recipe/config.
	reg.Register(workday.New(client))
	reg.Register(smartrecruiters.New(client))
	reg.Register(sitemapcrawler.New(client))

	// Schema.org JobPosting JSON-LD on listing/detail pages.
	reg.Register(structured.NewHTMLJSONLD(client, domain.SourceSchemaOrg))
	// Alias for seeds/admin still using "html" / "generic_html" without a recipe
	// (JSON-LD only boards). Prefer a recipe for non-JSON-LD HTML.
	reg.Register(structured.NewHTMLJSONLD(client, domain.SourceGenericHTML))

	// Legacy type strings that previously had dedicated packages still resolve
	// to the schema.org engine so existing rows keep crawling until recipes
	// are attached. New sources should use engine types only.
	for _, st := range []domain.SourceType{
		domain.SourceBrighterMonday,
		domain.SourceJobberman,
		domain.SourceMyJobMag,
		domain.SourceNjorku,
		domain.SourceCareers24,
		domain.SourcePNet,
		domain.SourceHostedBoards,
		domain.SourceSmartRecruitersPage,
	} {
		reg.Register(structured.NewHTMLJSONLD(client, st))
	}

	// Declarative feed/spec connectors from R2 definitions (still data-driven).
	if loader != nil {
		registerSpecConnectors(ctx, reg, loader, client)
		loader.Subscribe(definitions.TypeConnector, func(name, _ string) {
			util.Log(ctx).WithField("name", name).Info("connectors: spec changed, refreshing registry")
			registerSpecConnectors(ctx, reg, loader, client)
		})
	}

	util.Log(ctx).Info("connectors: engine-only registry (recipes for site-specific extract)")
	return reg
}

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
