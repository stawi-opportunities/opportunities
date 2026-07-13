package service

import (
	"context"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/arbeitnow"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/himalayas"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/jobicy"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/remoteok"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/sitemapcrawler"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/smartrecruiters"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/spec"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/structured"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/themuse"
	// Blank imports register each spec-driven impl into spec's
	// internal type→impl table via init().
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/htmllisting"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/jsonfeed"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/rssfeed"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/schemaorgjsonld"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/sitemap"
	_ "github.com/stawi-opportunities/opportunities/pkg/connectors/spec/xmlfeed"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/workday"
	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// BuildRegistry creates a connector Registry with structured extractors only:
// JSON APIs, ATS, sitemap+schema.org JobPosting, HTML JSON-LD, and R2 specs.
// There is no universal AI / stub connector path.
func BuildRegistry(ctx context.Context, client *httpx.Client, loader *definitions.R2Loader) *connectors.Registry {
	reg := connectors.NewRegistry()

	// Free public JSON job boards — complete records.
	reg.Register(remoteok.New())
	reg.Register(arbeitnow.New())
	reg.Register(jobicy.New())
	reg.Register(themuse.New())
	reg.Register(himalayas.New())

	// ATS structured connectors.
	reg.Register(workday.New(client))
	reg.Register(smartrecruiters.New(client))

	// Sitemap → schema.org JobPosting detail fetch.
	reg.Register(sitemapcrawler.New(client))

	// schema.org on listing/detail pages (no LLM).
	reg.Register(structured.NewHTMLJSONLD(client, domain.SourceSchemaOrg))
	for _, st := range []domain.SourceType{
		domain.SourceBrighterMonday,
		domain.SourceJobberman,
		domain.SourceMyJobMag,
		domain.SourceNjorku,
		domain.SourceCareers24,
		domain.SourcePNet,
		domain.SourceHostedBoards,
		domain.SourceGenericHTML,
		domain.SourceSmartRecruitersPage,
	} {
		reg.Register(structured.NewHTMLJSONLD(client, st))
	}

	// Declarative spec-driven connectors from R2.
	if loader != nil {
		registerSpecConnectors(ctx, reg, loader, client)
		loader.Subscribe(definitions.TypeConnector, func(name, _ string) {
			util.Log(ctx).WithField("name", name).Info("connectors: spec changed, refreshing registry")
			registerSpecConnectors(ctx, reg, loader, client)
		})
	}

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
