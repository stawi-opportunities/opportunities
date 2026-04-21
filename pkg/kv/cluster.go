package kv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ClusterSnapshot is the compact canonical view held in Redis so
// the canonical-merge stage can merge new variant fields into the
// existing cluster without re-reading the full canonicals partition.
// Small — ~1 KB per cluster.
type ClusterSnapshot struct {
	ClusterID      string    `json:"cluster_id"`
	CanonicalID    string    `json:"canonical_id,omitempty"`
	Slug           string    `json:"slug,omitempty"`
	Title          string    `json:"title,omitempty"`
	Company        string    `json:"company,omitempty"`
	Description    string    `json:"description,omitempty"`
	Country        string    `json:"country,omitempty"`
	Language       string    `json:"language,omitempty"`
	RemoteType     string    `json:"remote_type,omitempty"`
	EmploymentType string    `json:"employment_type,omitempty"`
	Seniority      string    `json:"seniority,omitempty"`
	SalaryMin      float64   `json:"salary_min,omitempty"`
	SalaryMax      float64   `json:"salary_max,omitempty"`
	Currency       string    `json:"currency,omitempty"`
	Category       string    `json:"category,omitempty"`
	QualityScore   float64   `json:"quality_score,omitempty"`
	Status         string    `json:"status,omitempty"`
	FirstSeenAt    time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt     time.Time `json:"last_seen_at,omitempty"`
	PostedAt       time.Time `json:"posted_at,omitempty"`
	ApplyURL       string    `json:"apply_url,omitempty"`
}

// ClusterStore wraps a Client with Get/Set of ClusterSnapshot.
type ClusterStore struct {
	c *Client
}

// NewClusterStore wraps a Client.
func NewClusterStore(c *Client) *ClusterStore { return &ClusterStore{c: c} }

const clusterPrefix = "cluster:"

// Get returns the snapshot if one exists.
func (s *ClusterStore) Get(ctx context.Context, clusterID string) (ClusterSnapshot, bool, error) {
	raw, err := s.c.rdb.Get(ctx, clusterPrefix+clusterID).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ClusterSnapshot{}, false, nil
		}
		return ClusterSnapshot{}, false, fmt.Errorf("kv: cluster get: %w", err)
	}
	var s0 ClusterSnapshot
	if err := json.Unmarshal(raw, &s0); err != nil {
		return ClusterSnapshot{}, false, fmt.Errorf("kv: cluster decode: %w", err)
	}
	return s0, true, nil
}

// Set writes the snapshot.
func (s *ClusterStore) Set(ctx context.Context, snap ClusterSnapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("kv: cluster encode: %w", err)
	}
	if err := s.c.rdb.Set(ctx, clusterPrefix+snap.ClusterID, raw, 0).Err(); err != nil {
		return fmt.Errorf("kv: cluster set: %w", err)
	}
	return nil
}
