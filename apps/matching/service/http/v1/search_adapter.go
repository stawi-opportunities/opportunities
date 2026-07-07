package v1

import (
	"context"
	"fmt"

	"github.com/stawi-opportunities/opportunities/pkg/pgsearch"
)

// PostgresSearch adapts *pgsearch.Search to the SearchIndex interface
// required by MatchHandler. All reads go through pgvector and the
// opportunities table.
type PostgresSearch struct {
	s *pgsearch.Search
}

// NewPostgresSearch wires the adapter against a read-only DB factory.
func NewPostgresSearch(db pgsearch.DB) *PostgresSearch {
	return &PostgresSearch{s: pgsearch.New(db)}
}

// Search returns the underlying *pgsearch.Search. Used by digest
// listers that share the same Postgres pool rather than reopening one.
func (m *PostgresSearch) Search() *pgsearch.Search { return m.s }

// KNNWithFilters implements SearchIndex. It composes the request-level
// filters (remote/salary/country) into a pgsearch.Filter and adds them
// onto whichever per-kind Filter the caller assembled earlier; the
// result is one parameterised KNN query.
//
// `SearchRequest.PreferredLocations` becomes a top-level country
// AnyOf; `SalaryMinFloor` becomes a top-level RangeMin on
// amount_min; `RemotePreference == "remote"` flips the RemoteOK
// widening clause.
func (m *PostgresSearch) KNNWithFilters(ctx context.Context, req SearchRequest) ([]SearchHit, error) {
	if len(req.Vector) == 0 {
		return nil, fmt.Errorf("search: empty vector")
	}
	f := pgsearch.Filter{}
	if req.RemotePreference == "remote" {
		f.RemoteOK = true
	}
	if req.SalaryMinFloor > 0 {
		f.RangeMin = append(f.RangeMin, pgsearch.RangeMin{Field: "amount_min", Value: float64(req.SalaryMinFloor)})
	}
	if len(req.PreferredLocations) > 0 {
		f.AnyOf = append(f.AnyOf, pgsearch.AnyOf{Field: "country", Values: req.PreferredLocations})
	}
	hits, err := m.s.KNNWithFilters(ctx, pgsearch.KNNRequest{
		Vector: req.Vector,
		Limit:  req.Limit,
		Filter: f,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHit{
			CanonicalID: h.CanonicalID,
			Slug:        h.Slug,
			Title:       h.Title,
			ApplyURL:    h.ApplyURL,
			Company:     h.Company,
			Score:       h.Score,
		})
	}
	return out, nil
}
