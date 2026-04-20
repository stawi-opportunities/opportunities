package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/pitabwire/util"

	"stawi.jobs/pkg/connectors"
	"stawi.jobs/pkg/connectors/httpx"
	"stawi.jobs/pkg/dedupe"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/normalize"
	"stawi.jobs/pkg/quality"
	"stawi.jobs/pkg/repository"
)

// CrawlResult summarises the outcome of processing a single CrawlRequest.
type CrawlResult struct {
	SourceID        string
	SourceType      domain.SourceType
	JobsFetched     int
	JobsAccepted    int
	JobsRejected    int
	JobsAIExtracted int
	PagesCrawled    int
	Duration        time.Duration
	Error           error
}

// Worker processes a single CrawlRequest through the full pipeline:
// fetch -> quality gate -> normalize -> batch write.
type Worker struct {
	registry   *connectors.Registry
	sourceRepo *repository.SourceRepository
	crawlRepo  *repository.CrawlRepository
	jobRepo    *repository.JobRepository
	rejectRepo *repository.RejectedJobRepository
	dedupeEng  *dedupe.Engine
	batch      *BatchBuffer
	extractor     *extraction.Extractor  // optional; nil disables AI extraction
	httpClient    *httpx.Client          // for fetching detail pages
	browserClient *httpx.BrowserClient   // optional; nil disables headless rendering
}

// NewWorker creates a Worker wired to the given repositories, registry, dedupe
// engine, batch buffer, an optional AI extractor (may be nil), and an HTTP
// client for fetching detail pages.
func NewWorker(
	registry *connectors.Registry,
	sourceRepo *repository.SourceRepository,
	crawlRepo *repository.CrawlRepository,
	jobRepo *repository.JobRepository,
	rejectRepo *repository.RejectedJobRepository,
	dedupeEng *dedupe.Engine,
	batch *BatchBuffer,
	extractor *extraction.Extractor,
	httpClient *httpx.Client,
	browserClient *httpx.BrowserClient,
) *Worker {
	return &Worker{
		registry:      registry,
		sourceRepo:    sourceRepo,
		crawlRepo:     crawlRepo,
		jobRepo:       jobRepo,
		rejectRepo:    rejectRepo,
		dedupeEng:     dedupeEng,
		batch:         batch,
		extractor:     extractor,
		httpClient:    httpClient,
		browserClient: browserClient,
	}
}

// ProcessRequest executes the crawl pipeline for the given CrawlRequest and
// returns a CrawlResult describing the outcome.
func (w *Worker) ProcessRequest(ctx context.Context, req domain.CrawlRequest) CrawlResult {
	start := time.Now()
	result := CrawlResult{
		SourceID:   req.SourceID,
		SourceType: req.SourceType,
	}

	// 1. Load source record.
	source, err := w.sourceRepo.GetByID(ctx, req.SourceID)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}

	// 2. Resolve the connector for this source type.
	connector, ok := w.registry.Get(source.Type)
	if !ok {
		result.Error = &errNoConnector{sourceType: source.Type}
		result.Duration = time.Since(start)
		return result
	}

	// 3. Start crawl iterator.
	iter := connector.Crawl(ctx, *source)

	scrapedAt := time.Now().UTC()

	// 4. Iterate over pages.
	for iter.Next(ctx) {
		result.PagesCrawled++

		// Save raw payload for this page.
		raw := iter.RawPayload()
		if len(raw) > 0 {
			payload := &domain.RawPayload{
				HTTPStatus: iter.HTTPStatus(),
				Body:       raw,
				FetchedAt:  scrapedAt,
			}
			// Best-effort -- ignore save errors to keep the pipeline moving.
			_ = w.crawlRepo.SaveRawPayload(ctx, payload)
		}

		// Process each job on this page.
		for _, extJob := range iter.Jobs() {
			result.JobsFetched++

			// For HTML-based sources, fetch the detail page and use AI to extract fields.
			if w.extractor != nil && needsAIExtraction(source.Type) && extJob.ApplyURL != "" {
				detailHTML, _, fetchErr := w.httpClient.Get(ctx, extJob.ApplyURL, nil)
				if fetchErr == nil {
					// If page has very little visible content, it's likely JS-rendered.
					// Fall back to headless browser if available.
					if w.browserClient != nil && !extraction.HasVisibleContent(string(detailHTML)) {
						util.Log(ctx).WithField("url", extJob.ApplyURL).Info("pipeline: thin content, using headless browser")
						if browserHTML, _, browserErr := w.browserClient.Get(ctx, extJob.ApplyURL); browserErr == nil {
							detailHTML = browserHTML
						}
					}
					if fields, aiErr := w.extractor.Extract(ctx, string(detailHTML), extJob.ApplyURL); aiErr == nil {
						mergeExtractedFields(&extJob, fields)
						result.JobsAIExtracted++
					} else {
						util.Log(ctx).WithError(aiErr).WithField("url", extJob.ApplyURL).Warn("pipeline: AI extraction failed")
					}
				} else {
					util.Log(ctx).WithError(fetchErr).WithField("url", extJob.ApplyURL).Warn("pipeline: fetch detail page failed")
				}
			}

			// Ensure apply_url has a fallback before quality gate
			quality.EnsureApplyURL(&extJob, extJob.SourceURL)
			if extJob.ApplyURL == "" {
				quality.EnsureApplyURL(&extJob, source.BaseURL)
			}

			// Quality gate -- applies to all sources.
			if qErr := quality.Check(extJob); qErr != nil {
				rejected := &domain.RejectedJob{
					SourceID:   req.SourceID,
					ExternalID: extJob.ExternalID,
					Reason:     qErr.Error(),
					RejectedAt: scrapedAt,
				}
				_ = w.rejectRepo.Create(ctx, rejected)
				result.JobsRejected++
				continue
			}

			// Normalize.
			variant := normalize.ExternalToVariant(extJob, req.SourceID, source.Country, string(source.Type), source.Language, scrapedAt)

			// Buffer for batch write.
			if bErr := w.batch.Add(ctx, variant); bErr != nil {
				// Non-fatal: record the error but continue processing.
				result.Error = bErr
			}
			result.JobsAccepted++
		}
	}

	// 5. Auto-discover new job sites from crawled pages (best-effort, async with timeout).
	if w.extractor != nil && len(iter.RawPayload()) > 0 {
		go func() {
			discoverCtx, discoverCancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer discoverCancel()
			sites, err := w.extractor.DiscoverSites(discoverCtx, string(iter.RawPayload()), source.BaseURL)
			if err != nil || len(sites) == 0 {
				return
			}
			for _, site := range sites {
				if site.URL == "" || site.URL == source.BaseURL {
					continue
				}
				newSource := &domain.Source{
					Type:             domain.SourceGenericHTML,
					BaseURL:          site.URL,
					Country:          site.Country,
					Status:           domain.SourceActive,
					Priority:         domain.PriorityNormal,
					CrawlIntervalSec: 7200,
					HealthScore:      1.0,
					Config:           "{}",
				}
				if err := w.sourceRepo.Upsert(context.Background(), newSource); err == nil {
					util.Log(discoverCtx).
						WithField("url", site.URL).
						WithField("name", site.Name).
						Info("pipeline: auto-discovered new job site")
				}
			}
		}()
	}

	// 6. Post-crawl health score and scheduling updates.
	now := time.Now().UTC()
	healthScore := source.HealthScore
	if iter.Err() != nil {
		result.Error = iter.Err()
		healthScore -= 0.2
		if healthScore < 0 {
			healthScore = 0
		}
	} else {
		healthScore += 0.1
		if healthScore > 1.0 {
			healthScore = 1.0
		}
	}

	nextCrawlAt := now.Add(time.Duration(source.CrawlIntervalSec) * time.Second)
	_ = w.sourceRepo.UpdateNextCrawl(ctx, source.ID, nextCrawlAt, now, healthScore)

	// Save cursor if the iterator produced one.
	if cursor := iter.Cursor(); cursor != nil {
		_ = w.sourceRepo.UpdateCrawlCursor(ctx, source.ID, string(cursor))
	}

	result.Duration = time.Since(start)
	return result
}

// mergeExtractedFields copies non-empty fields from JobFields into an ExternalJob,
// only filling fields that are currently empty (AI enrichment, not overwrite).
func mergeExtractedFields(job *domain.ExternalJob, fields *extraction.JobFields) {
	if job.Title == "" && fields.Title != "" {
		job.Title = fields.Title
	}
	if job.Company == "" && fields.Company != "" {
		job.Company = fields.Company
	}
	if job.LocationText == "" && fields.Location != "" {
		job.LocationText = fields.Location
	}
	if job.Description == "" && fields.Description != "" {
		job.Description = fields.Description
	}
	if job.ApplyURL == "" && fields.ApplyURL != "" {
		job.ApplyURL = fields.ApplyURL
	}
	if job.EmploymentType == "" && fields.EmploymentType != "" {
		job.EmploymentType = fields.EmploymentType
	}
	if job.RemoteType == "" && fields.RemoteType != "" {
		job.RemoteType = fields.RemoteType
	}
	if job.Currency == "" && fields.Currency != "" {
		job.Currency = fields.Currency
	}
	if fields.SalaryMin != "" && job.SalaryMin == 0 {
		_, _ = fmt.Sscanf(fields.SalaryMin, "%f", &job.SalaryMin)
	}
	if fields.SalaryMax != "" && job.SalaryMax == 0 {
		_, _ = fmt.Sscanf(fields.SalaryMax, "%f", &job.SalaryMax)
	}
	// Extended fields — always fill from AI
	if job.Seniority == "" && fields.Seniority != "" {
		job.Seniority = fields.Seniority
	}
	if len(job.Skills) == 0 && len(fields.Skills) > 0 {
		job.Skills = fields.Skills
	}
	if len(job.Roles) == 0 && len(fields.Roles) > 0 {
		job.Roles = fields.Roles
	}
	if len(job.Benefits) == 0 && len(fields.Benefits) > 0 {
		job.Benefits = fields.Benefits
	}
	if job.ContactName == "" && fields.ContactName != "" {
		job.ContactName = fields.ContactName
	}
	if job.ContactEmail == "" && fields.ContactEmail != "" {
		job.ContactEmail = fields.ContactEmail
	}
	if job.Department == "" && fields.Department != "" {
		job.Department = fields.Department
	}
	if job.Industry == "" && fields.Industry != "" {
		job.Industry = fields.Industry
	}
	if job.Education == "" && fields.Education != "" {
		job.Education = fields.Education
	}
	if job.Experience == "" && fields.Experience != "" {
		job.Experience = fields.Experience
	}
	if job.Deadline == "" && fields.Deadline != "" {
		job.Deadline = fields.Deadline
	}
	// Intelligence fields
	if job.UrgencyLevel == "" && fields.UrgencyLevel != "" {
		job.UrgencyLevel = fields.UrgencyLevel
	}
	if len(job.UrgencySignals) == 0 && len(fields.UrgencySignals) > 0 {
		job.UrgencySignals = fields.UrgencySignals
	}
	if job.HiringTimeline == "" && fields.HiringTimeline != "" {
		job.HiringTimeline = fields.HiringTimeline
	}
	if job.InterviewStages == 0 && fields.InterviewStages > 0 {
		job.InterviewStages = fields.InterviewStages
	}
	if !job.HasTakeHome && fields.HasTakeHome {
		job.HasTakeHome = fields.HasTakeHome
	}
	if job.FunnelComplexity == "" && fields.FunnelComplexity != "" {
		job.FunnelComplexity = fields.FunnelComplexity
	}
	if job.CompanySize == "" && fields.CompanySize != "" {
		job.CompanySize = fields.CompanySize
	}
	if job.FundingStage == "" && fields.FundingStage != "" {
		job.FundingStage = fields.FundingStage
	}
	if len(job.RequiredSkills) == 0 && len(fields.RequiredSkills) > 0 {
		job.RequiredSkills = fields.RequiredSkills
	}
	if len(job.NiceToHaveSkills) == 0 && len(fields.NiceToHaveSkills) > 0 {
		job.NiceToHaveSkills = fields.NiceToHaveSkills
	}
	if len(job.ToolsFrameworks) == 0 && len(fields.ToolsFrameworks) > 0 {
		job.ToolsFrameworks = fields.ToolsFrameworks
	}
	if job.GeoRestrictions == "" && fields.GeoRestrictions != "" {
		job.GeoRestrictions = fields.GeoRestrictions
	}
	if job.TimezoneReq == "" && fields.TimezoneReq != "" {
		job.TimezoneReq = fields.TimezoneReq
	}
	if job.ApplicationType == "" && fields.ApplicationType != "" {
		job.ApplicationType = fields.ApplicationType
	}
	if job.ATSPlatform == "" && fields.ATSPlatform != "" {
		job.ATSPlatform = fields.ATSPlatform
	}
	if job.RoleScope == "" && fields.RoleScope != "" {
		job.RoleScope = fields.RoleScope
	}
	if job.TeamSize == "" && fields.TeamSize != "" {
		job.TeamSize = fields.TeamSize
	}
	if job.ReportsTo == "" && fields.ReportsTo != "" {
		job.ReportsTo = fields.ReportsTo
	}
}

// needsAIExtraction returns true for HTML-based source types where regex parsing
// is unreliable and AI should be the primary field extractor.
func needsAIExtraction(st domain.SourceType) bool {
	switch st {
	case domain.SourceBrighterMonday, domain.SourceJobberman,
		domain.SourceMyJobMag, domain.SourceNjorku,
		domain.SourceCareers24, domain.SourcePNet,
		domain.SourceSchemaOrg, domain.SourceSitemap,
		domain.SourceHostedBoards, domain.SourceGenericHTML:
		return true
	default:
		return false
	}
}

// errNoConnector is returned when the registry has no connector for a source type.
type errNoConnector struct {
	sourceType domain.SourceType
}

func (e *errNoConnector) Error() string {
	return "no connector registered for source type: " + string(e.sourceType)
}
