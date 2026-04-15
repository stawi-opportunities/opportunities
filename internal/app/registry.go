package app

import (
	"stawi.jobs/internal/config"
	"stawi.jobs/internal/connectors"
	"stawi.jobs/internal/connectors/adzuna"
	"stawi.jobs/internal/connectors/generichtml"
	"stawi.jobs/internal/connectors/greenhouse"
	"stawi.jobs/internal/connectors/hostedboards"
	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/connectors/lever"
	"stawi.jobs/internal/connectors/schemaorg"
	"stawi.jobs/internal/connectors/serpapi"
	"stawi.jobs/internal/connectors/sitemap"
	"stawi.jobs/internal/connectors/smartrecruiters"
	"stawi.jobs/internal/connectors/smartrecruiterspage"
	"stawi.jobs/internal/connectors/usajobs"
	"stawi.jobs/internal/connectors/workday"
)

func NewConnectorRegistry(cfg config.Config) *connectors.Registry {
	httpClient := httpx.New(cfg.HTTPTimeout, cfg.UserAgent)
	return connectors.NewRegistry(
		adzuna.New(cfg, httpClient),
		serpapi.New(cfg, httpClient),
		usajobs.New(cfg, httpClient),
		smartrecruiters.New(httpClient),
		greenhouse.New(httpClient),
		lever.New(httpClient),
		workday.New(httpClient),
		smartrecruiterspage.New(httpClient),
		schemaorg.New(httpClient),
		sitemap.New(httpClient),
		hostedboards.New(httpClient),
		generichtml.New(httpClient),
	)
}
