package v1

import (
	"context"
	"fmt"
	"time"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/pgsearch"
)

// PostgresJobsLister implements NewJobsLister against opportunities.
//
// The lister speaks the same "what would a user see" baseline as the
// public API: status='active' AND hidden=false AND
// (deadline IS NULL OR deadline > now()). Anything else would put the
// digest out of sync with the search UI.
type PostgresJobsLister struct {
	s *pgsearch.Search
}

// NewPostgresJobsLister wires the lister against an existing Search
// adapter (so it reuses the matching service's already-warmed pool).
func NewPostgresJobsLister(s *pgsearch.Search) *PostgresJobsLister {
	return &PostgresJobsLister{s: s}
}

// ListNewJobs returns up to `limit` opportunities posted on/after
// `since`, optionally filtered by country + kinds.
func (l *PostgresJobsLister) ListNewJobs(ctx context.Context, since time.Time, country string, kinds []string, limit int) ([]eventsv1.DigestJob, error) {
	hits, err := l.s.ListNewJobs(ctx, since, country, kinds, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres jobs lister: %w", err)
	}
	out := make([]eventsv1.DigestJob, 0, len(hits))
	for _, h := range hits {
		out = append(out, eventsv1.DigestJob{
			CanonicalID: h.CanonicalID,
			Title:       h.Title,
			ApplyURL:    h.ApplyURL,
			Company:     h.Company,
			Country:     h.Country,
			Kind:        h.Kind,
			Slug:        h.Slug,
			PostedAt:    h.PostedAt,
		})
	}
	return out, nil
}

// PostgresWeeklyStatsLister implements WeeklyStatsLister against
// Postgres aggregations, served by three small SELECT … GROUP BY queries.
type PostgresWeeklyStatsLister struct {
	s *pgsearch.Search
}

// NewPostgresWeeklyStatsLister wires the lister.
func NewPostgresWeeklyStatsLister(s *pgsearch.Search) *PostgresWeeklyStatsLister {
	return &PostgresWeeklyStatsLister{s: s}
}

// GlobalStats fetches the three headline numbers (total + top-3
// country + top-3 kind buckets) for the past `since` window.
func (l *PostgresWeeklyStatsLister) GlobalStats(ctx context.Context, since time.Time) (eventsv1.DigestStats, error) {
	stats, err := l.s.GlobalStats(ctx, since)
	if err != nil {
		return eventsv1.DigestStats{}, fmt.Errorf("postgres weekly stats: %w", err)
	}
	return eventsv1.DigestStats{
		TotalNewThisWeek: stats.TotalNewThisWeek,
		TopCountries:     mapBuckets(stats.TopCountries),
		TopKinds:         mapBuckets(stats.TopKinds),
	}, nil
}

func mapBuckets(in []pgsearch.StatBucket) []eventsv1.DigestStatBucket {
	if len(in) == 0 {
		return nil
	}
	out := make([]eventsv1.DigestStatBucket, 0, len(in))
	for _, b := range in {
		out = append(out, eventsv1.DigestStatBucket{Code: b.Code, Count: b.Count})
	}
	return out
}
