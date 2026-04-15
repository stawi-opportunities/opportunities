package normalize

import (
	"testing"
	"time"

	"stawi.jobs/internal/domain"
)

func TestExternalToVariantDefaults(t *testing.T) {
	now := time.Now().UTC()
	v := ExternalToVariant(domain.ExternalJob{Title: "Backend Engineer", Company: "Acme", Description: "Build APIs"}, 12, "us", now)
	if v.ExternalJobID == "" {
		t.Fatal("expected generated external job id")
	}
	if v.Country != "US" {
		t.Fatalf("expected upper country, got %s", v.Country)
	}
	if v.ContentHash == "" {
		t.Fatal("expected content hash")
	}
}
