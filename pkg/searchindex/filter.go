package searchindex

import (
	"fmt"
	"strings"
)

// Filter renders to a Manticore WHERE clause via .SQL().
type Filter struct {
	Kind     string
	AnyOf    []AnyOf
	RangeMin []RangeMin
	GeoDist  *GeoDist
	OrTerm   string
}

type AnyOf struct {
	Field  string
	Values []string
}

type RangeMin struct {
	Field string
	Value float64
}

type GeoDist struct {
	Lat, Lon float64
	RadiusKm int
}

// SQL renders the filter as a Manticore WHERE clause string. Empty
// filter returns "" (caller can decide whether to omit a WHERE).
func (f Filter) SQL() string {
	parts := []string{}
	if f.Kind != "" {
		parts = append(parts, "kind = '"+f.Kind+"'")
	}
	for _, a := range f.AnyOf {
		if len(a.Values) == 0 {
			continue
		}
		quoted := make([]string, len(a.Values))
		for i, v := range a.Values {
			quoted[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		}
		parts = append(parts, a.Field+" IN ("+strings.Join(quoted, ",")+")")
	}
	for _, r := range f.RangeMin {
		parts = append(parts, fmt.Sprintf("%s >= %g", r.Field, r.Value))
	}
	if f.GeoDist != nil {
		parts = append(parts, fmt.Sprintf("GEODIST(lat, lon, %g, %g, {in=deg, out=km}) <= %d",
			f.GeoDist.Lat, f.GeoDist.Lon, f.GeoDist.RadiusKm))
	}
	clause := strings.Join(parts, " AND ")
	if f.OrTerm != "" {
		if clause != "" {
			clause = "(" + clause + ") OR " + f.OrTerm
		} else {
			clause = f.OrTerm
		}
	}
	return clause
}
