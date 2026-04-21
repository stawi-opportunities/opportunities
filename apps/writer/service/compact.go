package service

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// Compactor merges small per-partition Parquet files and dedups by event_id,
// keeping the row with the earliest occurred_at. Small files produced during
// the same UTC day are merged per secondary-partition directory (e.g. src=acme/).
type Compactor struct {
	s3       *s3.Client
	reader   *eventlog.Reader
	uploader *eventlog.Uploader
	bucket   string
}

// NewCompactor returns a Compactor backed by the given S3 client, reader, uploader,
// and bucket.
func NewCompactor(cli *s3.Client, r *eventlog.Reader, u *eventlog.Uploader, bucket string) *Compactor {
	return &Compactor{s3: cli, reader: r, uploader: u, bucket: bucket}
}

// CompactHourlyInput parameterises a single CompactHourly call.
type CompactHourlyInput struct {
	// Collection is the top-level Parquet partition name (e.g. "variants").
	Collection string
	// Hour is the UTC hour whose day-partition is compacted.
	Hour time.Time
}

// CompactHourlyResult summarises what CompactHourly did.
type CompactHourlyResult struct {
	RowsBefore   int
	RowsAfter    int
	FilesBefore  int
	FilesAfter   int
	FilesDeleted int
}

// CompactHourly reads every Parquet file under <collection>/dt=<YYYY-MM-DD>/,
// groups them by secondary partition (e.g. src=…/), merges each group,
// dedups by event_id (earliest occurred_at wins), writes one merged file per
// group, and deletes the originals.
func (c *Compactor) CompactHourly(ctx context.Context, in CompactHourlyInput) (CompactHourlyResult, error) {
	dt := in.Hour.UTC().Format("2006-01-02")
	switch in.Collection {
	case "variants":
		return compactHourlyGeneric[eventsv1.VariantIngestedV1](ctx, c, in.Collection, dt,
			func(r eventsv1.VariantIngestedV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "canonicals":
		return compactHourlyGeneric[eventsv1.CanonicalUpsertedV1](ctx, c, in.Collection, dt,
			func(r eventsv1.CanonicalUpsertedV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "canonicals_expired":
		return compactHourlyGeneric[eventsv1.CanonicalExpiredV1](ctx, c, in.Collection, dt,
			func(r eventsv1.CanonicalExpiredV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "embeddings":
		return compactHourlyGeneric[eventsv1.EmbeddingV1](ctx, c, in.Collection, dt,
			func(r eventsv1.EmbeddingV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "translations":
		return compactHourlyGeneric[eventsv1.TranslationV1](ctx, c, in.Collection, dt,
			func(r eventsv1.TranslationV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "published":
		return compactHourlyGeneric[eventsv1.PublishedV1](ctx, c, in.Collection, dt,
			func(r eventsv1.PublishedV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "crawl_page_completed":
		return compactHourlyGeneric[eventsv1.CrawlPageCompletedV1](ctx, c, in.Collection, dt,
			func(r eventsv1.CrawlPageCompletedV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "sources_discovered":
		return compactHourlyGeneric[eventsv1.SourceDiscoveredV1](ctx, c, in.Collection, dt,
			func(r eventsv1.SourceDiscoveredV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "candidates_cv":
		return compactHourlyGeneric[eventsv1.CVExtractedV1](ctx, c, in.Collection, dt,
			func(r eventsv1.CVExtractedV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "candidates_improvements":
		return compactHourlyGeneric[eventsv1.CVImprovedV1](ctx, c, in.Collection, dt,
			func(r eventsv1.CVImprovedV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "candidates_preferences":
		return compactHourlyGeneric[eventsv1.PreferencesUpdatedV1](ctx, c, in.Collection, dt,
			func(r eventsv1.PreferencesUpdatedV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "candidates_embeddings":
		return compactHourlyGeneric[eventsv1.CandidateEmbeddingV1](ctx, c, in.Collection, dt,
			func(r eventsv1.CandidateEmbeddingV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	case "candidates_matches_ready":
		return compactHourlyGeneric[eventsv1.MatchesReadyV1](ctx, c, in.Collection, dt,
			func(r eventsv1.MatchesReadyV1) (string, time.Time) { return r.EventID, r.OccurredAt.UTC() })
	default:
		return CompactHourlyResult{}, fmt.Errorf("compact: unknown collection %q", in.Collection)
	}
}

// compactHourlyGeneric is the generic implementation shared by all collection cases.
// It lists all objects under prefix, groups by one level of sub-directory, then
// merges and dedups each group.
func compactHourlyGeneric[T any](
	ctx context.Context,
	c *Compactor,
	collection, dt string,
	kf func(T) (string, time.Time),
) (CompactHourlyResult, error) {
	prefix := collection + "/dt=" + dt + "/"

	// Collect all object keys under the day partition.
	var keys []string
	cursor := ""
	for {
		page, err := c.reader.ListNewObjects(ctx, prefix, cursor, 1000)
		if err != nil {
			return CompactHourlyResult{}, fmt.Errorf("compact: list %q: %w", prefix, err)
		}
		if len(page) == 0 {
			break
		}
		for _, o := range page {
			if o.Key != nil {
				keys = append(keys, *o.Key)
			}
		}
		// Safely advance cursor to the last non-nil key in this page.
		// A nil Key in the tail position would cause a panic; tracking
		// the last collected key keeps the loop robust against that.
		lastKey := ""
		for i := len(page) - 1; i >= 0; i-- {
			if page[i].Key != nil {
				lastKey = *page[i].Key
				break
			}
		}
		if lastKey == "" {
			break // entire page had nil keys — abort rather than infinite loop
		}
		cursor = lastKey
	}
	if len(keys) == 0 {
		return CompactHourlyResult{}, nil
	}

	// Group keys by their immediate sub-directory after the dt= prefix
	// (e.g. "src=acme/"). Files directly in the dt= prefix go into group "".
	groups := map[string][]string{}
	for _, k := range keys {
		rel := strings.TrimPrefix(k, prefix)
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) != 2 {
			groups[""] = append(groups[""], k)
			continue
		}
		groups[parts[0]] = append(groups[parts[0]], k)
	}

	var result CompactHourlyResult
	result.FilesBefore = len(keys)

	for secondaryDir, groupKeys := range groups {
		sort.Strings(groupKeys)

		// Read and merge all rows in this group, deduping by event_id.
		merged := map[string]T{}
		earliest := map[string]time.Time{}
		rowsRead := 0

		for _, k := range groupKeys {
			body, err := c.reader.Get(ctx, k)
			if err != nil {
				return result, fmt.Errorf("compact: get %q: %w", k, err)
			}
			rows, err := eventlog.ReadParquet[T](body)
			if err != nil {
				return result, fmt.Errorf("compact: read parquet %q: %w", k, err)
			}
			rowsRead += len(rows)
			for _, r := range rows {
				id, occ := kf(r)
				if id == "" {
					// Rows without an event_id are treated as unique.
					id = "__sans_id_" + xid.New().String()
				}
				if prev, ok := earliest[id]; !ok || occ.Before(prev) {
					merged[id] = r
					earliest[id] = occ
				}
			}
		}

		rowsAfter := len(merged)
		result.RowsBefore += rowsRead
		result.RowsAfter += rowsAfter

		if rowsAfter == 0 {
			continue
		}

		// Sort merged rows deterministically by event_id for stable output.
		ids := make([]string, 0, rowsAfter)
		for id := range merged {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		ordered := make([]T, 0, rowsAfter)
		for _, id := range ids {
			ordered = append(ordered, merged[id])
		}

		body, err := eventlog.WriteParquet(ordered)
		if err != nil {
			return result, fmt.Errorf("compact: write parquet: %w", err)
		}

		var mergedKey string
		if secondaryDir == "" {
			mergedKey = path.Join(prefix, "compact-"+xid.New().String()+".parquet")
		} else {
			mergedKey = path.Join(prefix, secondaryDir, "compact-"+xid.New().String()+".parquet")
		}
		if _, err := c.uploader.Put(ctx, mergedKey, body); err != nil {
			return result, fmt.Errorf("compact: upload %q: %w", mergedKey, err)
		}
		result.FilesAfter++

		// Delete the original small files.
		for _, k := range groupKeys {
			_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(c.bucket),
				Key:    aws.String(k),
			})
			if err != nil {
				util.Log(ctx).WithError(err).WithField("key", k).
					Warn("compact: delete source failed; orphaned")
				continue
			}
			result.FilesDeleted++
		}
	}

	return result, nil
}
