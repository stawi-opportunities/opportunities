package service

import (
	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/arbeitnow"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/greenhouse"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/himalayas"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/jobicy"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/remoteok"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/sitemapcrawler"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/smartrecruiters"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/themuse"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/universal"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/workday"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
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

	return reg
}
