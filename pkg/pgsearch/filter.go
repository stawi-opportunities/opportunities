// Package pgsearch exposes a small Postgres-native filter algebra
// (Kind / AnyOf / RangeMin / RemoteOK) plus a Search adapter that
// renders pgvector KNN + WHERE-clause queries against the
// `opportunities` canonical table.
//
// All per-kind matchers (job/scholarship/tender/deal/funding) build a
// pgsearch.Filter; the matching HTTP handler hands it off to the
// Search adapter, which produces a single parameterised SQL query.
// Top-level columns (kind, country, amount_min, …) are matched
// directly; per-kind sparse facets (employment_type, seniority,
// degree_level, …) live in `attributes` jsonb and are matched via
// `attributes->>'<field>' = ANY(...)`.
package pgsearch

import (
	"fmt"
	"strings"
)

// Filter is the kind-agnostic filter the matching layer hands to the
// Search adapter. Empty fields are skipped.
//
// AttributeAnyOf is the JSONB equivalent of AnyOf — same shape but
// rendered against `attributes->>'<field>'`. Top-level columns
// (kind, country, region, city) go in AnyOf; per-kind facets stored
// in the JSONB Attributes column (employment_type, seniority,
// degree_level, field_of_study, …) go in AttributeAnyOf.
type Filter struct {
	Kind           string
	AnyOf          []AnyOf
	AttributeAnyOf []AnyOf
	RangeMin       []RangeMin
	RemoteOK       bool
}

// AnyOf renders to either `<Field> = ANY($n)` (top-level columns)
// or `attributes->>'<Field>' = ANY($n)` (JSONB attributes), depending
// on which slice it lives in.
type AnyOf struct {
	Field  string
	Values []string
}

// RangeMin renders to `<Field> >= $n`.
type RangeMin struct {
	Field string
	Value float64
}

// Build renders the filter as a SQL fragment (no leading WHERE) plus
// the positional argument list ready for pgx.
//
// `startAt` is the starting placeholder index — pass len(existingArgs)+1
// so the rendered fragment slots cleanly into a larger query.
//
// Returns ("", nil) for an empty filter — caller decides whether to
// omit the WHERE clause entirely.
func (f Filter) Build(startAt int) (string, []any) {
	var parts []string
	var args []any
	n := startAt

	if f.Kind != "" {
		parts = append(parts, fmt.Sprintf("kind = $%d", n))
		args = append(args, f.Kind)
		n++
	}
	for _, a := range f.AnyOf {
		if len(a.Values) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s = ANY($%d)", a.Field, n))
		args = append(args, a.Values)
		n++
	}
	for _, a := range f.AttributeAnyOf {
		if len(a.Values) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("attributes->>'%s' = ANY($%d)", a.Field, n))
		args = append(args, a.Values)
		n++
	}
	for _, r := range f.RangeMin {
		parts = append(parts, fmt.Sprintf("%s >= $%d", r.Field, n))
		args = append(args, r.Value)
		n++
	}
	if f.RemoteOK {
		// RemoteOK widens the filter to include remote opportunities
		// regardless of country — wrapped in parens so it OR-combines
		// against the country AnyOf above without disturbing AND-
		// composition with the rest.
		parts = append(parts, "(remote = true)")
	}
	return strings.Join(parts, " AND "), args
}
