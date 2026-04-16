package service

import (
	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/arbeitnow"
	"stawi.jobs/pkg/connectors/greenhouse"
	"stawi.jobs/pkg/connectors/himalayas"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/connectors/jobicy"
	"stawi.jobs/pkg/connectors/remoteok"
	"stawi.jobs/pkg/connectors/smartrecruiters"
	"stawi.jobs/pkg/connectors/themuse"
	"stawi.jobs/pkg/connectors/universal"
	"stawi.jobs/pkg/connectors/workday"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
)

// BuildRegistry creates a connector Registry with all available connectors
// registered. When extractor is non-nil the universal AI connector is
// registered for all HTML-based source types; otherwise those types are
// skipped (they cannot work without AI link discovery).
func BuildRegistry(client *httpx.Client, extractor *extraction.Extractor) *connectors.Registry {
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
			domain.SourceSitemap,
			domain.SourceHostedBoards,
			domain.SourceGenericHTML,
			domain.SourceSmartRecruitersPage,
		} {
			reg.Register(universal.NewTyped(client, extractor, st))
		}
	}

	return reg
}
