package service

import (
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/arbeitnow"
	"stawi.jobs/pkg/connectors/brightermonday"
	"stawi.jobs/pkg/connectors/careers24"
	"stawi.jobs/pkg/connectors/findwork"
	"stawi.jobs/pkg/connectors/greenhouse"
	"stawi.jobs/pkg/connectors/himalayas"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/connectors/jobberman"
	"stawi.jobs/pkg/connectors/jobicy"
	"stawi.jobs/pkg/connectors/lever"
	"stawi.jobs/pkg/connectors/myjobmag"
	"stawi.jobs/pkg/connectors/njorku"
	"stawi.jobs/pkg/connectors/pnet"
	"stawi.jobs/pkg/connectors/remoteok"
	"stawi.jobs/pkg/connectors/schemaorg"
	"stawi.jobs/pkg/connectors/smartrecruiters"
	"stawi.jobs/pkg/connectors/themuse"
	"stawi.jobs/pkg/connectors/workday"
)

// BuildRegistry creates a connector Registry with all available connectors
// registered. Connectors that require an HTTP client receive the provided one;
// the rest use their own internal clients.
func BuildRegistry(client *httpx.Client) *connectors.Registry {
	reg := connectors.NewRegistry()

	// Free JSON API connectors (no httpx.Client dependency).
	reg.Register(remoteok.New())
	reg.Register(arbeitnow.New())
	reg.Register(jobicy.New())
	reg.Register(themuse.New())
	reg.Register(himalayas.New())
	reg.Register(findwork.New())

	// African job board connectors (no httpx.Client dependency).
	reg.Register(brightermonday.New())
	reg.Register(jobberman.New())
	reg.Register(myjobmag.New())
	reg.Register(njorku.New())
	reg.Register(careers24.New())
	reg.Register(pnet.New())

	// ATS / structured-data connectors (require httpx.Client).
	reg.Register(greenhouse.New(client))
	reg.Register(lever.New(client))
	reg.Register(workday.New(client))
	reg.Register(smartrecruiters.New(client))
	reg.Register(schemaorg.New(client))

	return reg
}
