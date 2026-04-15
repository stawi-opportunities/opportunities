package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"stawi.jobs/internal/config"
	"stawi.jobs/internal/domain"
)

type Discovery struct {
	cfg   config.Config
	store domain.SourceStore
	log   *slog.Logger
}

func NewDiscovery(cfg config.Config, store domain.SourceStore, log *slog.Logger) *Discovery {
	return &Discovery{cfg: cfg, store: store, log: log}
}

func (d *Discovery) Run(ctx context.Context) error {
	if err := d.seed(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := d.seed(ctx); err != nil {
				d.log.Error("periodic seed failed", "error", err.Error())
			}
		}
	}
}

func (d *Discovery) seed(ctx context.Context) error {
	now := time.Now().UTC()
	seed := []domain.Source{
		{Type: domain.SourceAdzuna, BaseURL: "https://api.adzuna.com", Country: d.cfg.DefaultCountry, Status: domain.SourceActive, CrawlIntervalSec: 1800, NextCrawlAt: now},
		{Type: domain.SourceSerpAPI, BaseURL: "https://serpapi.com", Country: d.cfg.DefaultCountry, Status: domain.SourceActive, CrawlIntervalSec: 900, NextCrawlAt: now},
		{Type: domain.SourceUSAJobs, BaseURL: "https://data.usajobs.gov", Country: "US", Status: domain.SourceActive, CrawlIntervalSec: 3600, NextCrawlAt: now},
	}
	for _, s := range seed {
		if _, err := d.store.UpsertSource(ctx, s); err != nil {
			return fmt.Errorf("upsert seed source: %w", err)
		}
	}
	candidates := []string{
		"https://boards.greenhouse.io/stripe",
		"https://jobs.lever.co/netflix",
		"https://careers.smartrecruiters.com/SmartRecruiters",
		"https://jobs.workday.com/en-US/search",
	}
	for _, u := range candidates {
		typ := classify(u)
		src := domain.Source{Type: typ, BaseURL: u, Country: d.cfg.DefaultCountry, Status: domain.SourceActive, CrawlIntervalSec: 21600, NextCrawlAt: now}
		if _, err := d.store.UpsertSource(ctx, src); err != nil {
			d.log.Warn("upsert discovered source failed", "url", u, "error", err.Error())
		}
	}
	d.log.Info("source discovery completed", "seed_sources", len(seed), "candidates", len(candidates))
	return nil
}

func classify(url string) domain.SourceType {
	u := strings.ToLower(url)
	switch {
	case strings.Contains(u, "greenhouse"):
		return domain.SourceGreenhouse
	case strings.Contains(u, "lever"):
		return domain.SourceLever
	case strings.Contains(u, "workday"):
		return domain.SourceWorkday
	case strings.Contains(u, "smartrecruiters"):
		return domain.SourceSmartRecruitersPage
	default:
		return domain.SourceSchemaOrg
	}
}
