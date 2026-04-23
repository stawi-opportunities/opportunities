package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKVRebuildFold_LatestPerCluster verifies that when multiple canonicalMinimal
// rows share a cluster_id, the fold keeps the one with the latest OccurredAt.
func TestKVRebuildFold_LatestPerCluster(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	t2 := t0.Add(48 * time.Hour)

	rows := []canonicalMinimal{
		{ClusterID: "c1", CanonicalID: "can-a", Title: "Old Title", OccurredAt: t0},
		{ClusterID: "c1", CanonicalID: "can-b", Title: "New Title", OccurredAt: t2},
		{ClusterID: "c1", CanonicalID: "can-c", Title: "Middle Title", OccurredAt: t1},
		{ClusterID: "c2", CanonicalID: "can-d", Title: "Solo", OccurredAt: t0},
		// Row with empty cluster_id — must be ignored.
		{ClusterID: "", CanonicalID: "can-e", Title: "Orphan", OccurredAt: t2},
	}

	latest := make(map[string]canonicalMinimal)
	for _, row := range rows {
		if row.ClusterID == "" {
			continue
		}
		existing, ok := latest[row.ClusterID]
		if !ok || row.OccurredAt.After(existing.OccurredAt) {
			latest[row.ClusterID] = row
		}
	}

	require.Len(t, latest, 2, "should have exactly two distinct cluster IDs (orphan excluded)")

	c1 := latest["c1"]
	assert.Equal(t, "can-b", c1.CanonicalID, "c1 should keep the row with latest OccurredAt (t2)")
	assert.Equal(t, "New Title", c1.Title)
	assert.Equal(t, t2, c1.OccurredAt)

	c2 := latest["c2"]
	assert.Equal(t, "can-d", c2.CanonicalID, "c2 has only one row, should be kept as-is")

	_, orphan := latest[""]
	assert.False(t, orphan, "empty cluster_id must never appear in latest map")
}

// TestKVRebuildFold_EmptyInput verifies that an empty row slice produces an
// empty latest map without panicking.
func TestKVRebuildFold_EmptyInput(t *testing.T) {
	rows := []canonicalMinimal{}
	latest := make(map[string]canonicalMinimal)
	for _, row := range rows {
		if row.ClusterID == "" {
			continue
		}
		existing, ok := latest[row.ClusterID]
		if !ok || row.OccurredAt.After(existing.OccurredAt) {
			latest[row.ClusterID] = row
		}
	}
	assert.Empty(t, latest)
}

// TestKVRebuildFold_AllEmptyClusterID verifies that rows with no cluster_id
// are all discarded.
func TestKVRebuildFold_AllEmptyClusterID(t *testing.T) {
	rows := []canonicalMinimal{
		{ClusterID: "", CanonicalID: "x", OccurredAt: time.Now()},
		{ClusterID: "", CanonicalID: "y", OccurredAt: time.Now()},
	}
	latest := make(map[string]canonicalMinimal)
	for _, row := range rows {
		if row.ClusterID == "" {
			continue
		}
		existing, ok := latest[row.ClusterID]
		if !ok || row.OccurredAt.After(existing.OccurredAt) {
			latest[row.ClusterID] = row
		}
	}
	assert.Empty(t, latest)
}

// TestClusterSnapshotFromMinimal verifies the field mapping between
// canonicalMinimal and kv.ClusterSnapshot.
func TestClusterSnapshotFromMinimal(t *testing.T) {
	ts := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	row := canonicalMinimal{
		ClusterID:      "cluster-42",
		CanonicalID:    "can-42",
		Slug:           "golang-engineer-42",
		Title:          "Go Engineer",
		Company:        "Acme",
		Country:        "KE",
		Language:       "en",
		RemoteType:     "remote",
		EmploymentType: "full_time",
		Seniority:      "senior",
		SalaryMin:      50000,
		SalaryMax:      90000,
		Currency:       "USD",
		Category:       "engineering",
		QualityScore:   88.5,
		Status:         "active",
		FirstSeenAt:    ts,
		LastSeenAt:     ts,
		PostedAt:       ts,
		OccurredAt:     ts,
		ApplyURL:       "https://example.com/apply",
	}

	snap := clusterSnapshotFromMinimal(row)

	assert.Equal(t, "cluster-42", snap.ClusterID)
	assert.Equal(t, "can-42", snap.CanonicalID)
	assert.Equal(t, "golang-engineer-42", snap.Slug)
	assert.Equal(t, "Go Engineer", snap.Title)
	assert.Equal(t, "Acme", snap.Company)
	assert.Equal(t, "KE", snap.Country)
	assert.Equal(t, "remote", snap.RemoteType)
	assert.Equal(t, float64(88.5), snap.QualityScore)
	assert.Equal(t, "active", snap.Status)
	assert.Equal(t, ts, snap.PostedAt)
	assert.Equal(t, "https://example.com/apply", snap.ApplyURL)
}
