package repository

import (
	"context"

	"gorm.io/gorm"
)

// FacetEntry is a single (key, count) tuple for one facet dimension.
type FacetEntry struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// Facets groups counts by dimension. Each slice is sorted by Count DESC.
type Facets struct {
	Category       []FacetEntry `json:"category"`
	RemoteType     []FacetEntry `json:"remote_type"`
	EmploymentType []FacetEntry `json:"employment_type"`
	Seniority      []FacetEntry `json:"seniority"`
	Country        []FacetEntry `json:"country"`
}

// FacetRepository reads and refreshes the mv_job_facets materialized view.
type FacetRepository struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewFacetRepository constructs a FacetRepository bound to the given db func.
func NewFacetRepository(db func(ctx context.Context, readOnly bool) *gorm.DB) *FacetRepository {
	return &FacetRepository{db: db}
}

// Read returns all facet buckets sorted by count DESC within each dimension.
// The view is populated by Refresh (usually on a 5-min cadence).
func (r *FacetRepository) Read(ctx context.Context) (Facets, error) {
	type row struct {
		Dim string
		Key string
		N   int64
	}
	var rows []row
	err := r.db(ctx, true).
		Table("mv_job_facets").
		Select("dim, key, n").
		Order("dim, n DESC").
		Scan(&rows).Error
	if err != nil {
		return Facets{}, err
	}
	out := Facets{
		Category:       []FacetEntry{},
		RemoteType:     []FacetEntry{},
		EmploymentType: []FacetEntry{},
		Seniority:      []FacetEntry{},
		Country:        []FacetEntry{},
	}
	for _, row := range rows {
		e := FacetEntry{Key: row.Key, Count: row.N}
		switch row.Dim {
		case "category":
			out.Category = append(out.Category, e)
		case "remote_type":
			out.RemoteType = append(out.RemoteType, e)
		case "employment_type":
			out.EmploymentType = append(out.EmploymentType, e)
		case "seniority":
			out.Seniority = append(out.Seniority, e)
		case "country":
			out.Country = append(out.Country, e)
		}
	}
	return out, nil
}

// Refresh recomputes mv_job_facets concurrently so readers aren't blocked.
// Call on a schedule (every 5 minutes in production).
func (r *FacetRepository) Refresh(ctx context.Context) error {
	return r.db(ctx, false).
		Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv_job_facets").
		Error
}
