package main

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/util"
	"gorm.io/gorm"

	"stawi.jobs/pkg/archive"
)

// reconcileOrphans walks clusters/* in the archive bucket and deletes
// any cluster directory whose cluster_id doesn't appear in
// canonical_jobs. Catches two failure modes:
//  1. Archive write succeeded, DB commit failed (orphan bundle).
//  2. Purge sweeper missed some objects due to partial failure.
//
// Runs nightly. Inexpensive because most clusters have live canonicals.
func reconcileOrphans(
	ctx context.Context,
	client *s3.Client,
	bucket string,
	db func(ctx context.Context, readOnly bool) *gorm.DB,
	arch archive.Archive,
) {
	log := util.Log(ctx)

	// 1. List every cluster prefix.
	var continuation *string
	seen := map[string]bool{}
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String("clusters/"),
			Delimiter:         aws.String("/"),
			ContinuationToken: continuation,
		})
		if err != nil {
			log.WithError(err).Error("reconcile: list failed")
			return
		}
		for _, pfx := range out.CommonPrefixes {
			if pfx.Prefix == nil {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(*pfx.Prefix, "clusters/"), "/")
			if id == "" {
				continue
			}
			seen[id] = true
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuation = out.NextContinuationToken
	}

	if len(seen) == 0 {
		return
	}

	// 2. For each seen cluster, check DB; orphans get deleted.
	clusterIDs := make([]string, 0, len(seen))
	for id := range seen {
		clusterIDs = append(clusterIDs, id)
	}
	var alive []string
	if err := db(ctx, true).
		Table("canonical_jobs").
		Where("cluster_id IN ?", clusterIDs).
		Pluck("cluster_id", &alive).Error; err != nil {
		log.WithError(err).Error("reconcile: DB lookup failed")
		return
	}
	aliveSet := map[string]bool{}
	for _, id := range alive {
		aliveSet[id] = true
	}

	orphans := 0
	for _, id := range clusterIDs {
		if aliveSet[id] {
			continue
		}
		if err := arch.DeleteCluster(ctx, id); err != nil {
			log.WithError(err).WithField("cluster_id", id).
				Warn("reconcile: delete orphan failed")
			continue
		}
		orphans++
	}
	if orphans > 0 {
		log.WithField("orphan_clusters_deleted", orphans).Info("reconcile: sweep complete")
	}
}
