package geocode

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestGeocode_KnownCity(t *testing.T) {
	g := New()
	loc, ok := g.Lookup("Nairobi", "KE")
	if !ok {
		t.Fatal("expected Nairobi/KE to resolve")
	}
	if loc.Lat < -2 || loc.Lat > 0 || loc.Lon < 36 || loc.Lon > 38 {
		t.Errorf("Nairobi coords look wrong: %+v", loc)
	}
}

func TestGeocode_Miss(t *testing.T) {
	g := New()
	if _, ok := g.Lookup("Atlantis", ""); ok {
		t.Fatal("Atlantis should not resolve")
	}
}

func TestGeocode_CaseInsensitive(t *testing.T) {
	g := New()
	if _, ok := g.Lookup("nAirObI", "KE"); !ok {
		t.Fatal("expected case-insensitive match")
	}
}

// TestEnrich_PopulatesCoords confirms that a record carrying a
// recognised city but no coordinates gets Lat/Lon filled in.
func TestEnrich_PopulatesCoords(t *testing.T) {
	g := New()
	opp := &domain.ExternalOpportunity{
		AnchorLocation: &domain.Location{City: "Nairobi", Country: "KE"},
	}
	g.Enrich(opp)
	if !opp.AnchorLocation.HasCoords() {
		t.Fatal("expected coords to be populated")
	}
}

// TestEnrich_RespectsExistingCoords verifies that a caller-supplied
// (Lat, Lon) survives the Enrich pass — extraction wins over the
// gazetteer when both are available.
func TestEnrich_RespectsExistingCoords(t *testing.T) {
	g := New()
	opp := &domain.ExternalOpportunity{
		AnchorLocation: &domain.Location{
			City:    "Nairobi",
			Country: "KE",
			Lat:     -1.0,
			Lon:     36.0,
		},
	}
	g.Enrich(opp)
	if opp.AnchorLocation.Lat != -1.0 || opp.AnchorLocation.Lon != 36.0 {
		t.Errorf("expected existing coords preserved, got %+v", opp.AnchorLocation)
	}
}

// TestEnrich_NilAnchor short-circuits cleanly. A valid record can
// omit AnchorLocation entirely (global opportunity); Enrich must
// not panic.
func TestEnrich_NilAnchor(t *testing.T) {
	g := New()
	opp := &domain.ExternalOpportunity{}
	g.Enrich(opp) // must not panic
}

// TestEnrich_UnknownCity leaves coords zero. The downstream radius
// query will skip this record from "near me" results, which is the
// correct behaviour.
func TestEnrich_UnknownCity(t *testing.T) {
	g := New()
	opp := &domain.ExternalOpportunity{
		AnchorLocation: &domain.Location{City: "Atlantis", Country: "XX"},
	}
	g.Enrich(opp)
	if opp.AnchorLocation.HasCoords() {
		t.Errorf("unknown city should not produce coords, got %+v", opp.AnchorLocation)
	}
}
