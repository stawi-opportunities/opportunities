package ats

import "testing"

func TestDemoTalentRanks(t *testing.T) {
	d := NewDemoTalent()
	hits, err := d.ListForJob(t.Context(), "", "", "", "Go backend engineer", "Postgres Kubernetes payments", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	// Top hit should mention go/backend-ish profiles with higher score than pure designer for this query.
	if hits[0].Score < 0.4 {
		t.Fatalf("unexpected low score %+v", hits[0])
	}
}

func TestSeedDemoWorkspace(t *testing.T) {
	svc, ctx := testService(t)
	if err := SeedDemoWorkspace(ctx, svc); err != nil {
		t.Fatal(err)
	}
	jobs, err := svc.ListJobs(ctx, "")
	if err != nil || len(jobs) == 0 {
		t.Fatalf("jobs: %v %d", err, len(jobs))
	}
	// second seed is no-op
	if err := SeedDemoWorkspace(ctx, svc); err != nil {
		t.Fatal(err)
	}
	apps, err := svc.ListApplications(ctx, jobs[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) == 0 {
		t.Fatal("expected seeded applications from talent")
	}
}
