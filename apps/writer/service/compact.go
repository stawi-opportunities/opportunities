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
// CompactDailyInput parameterises a single CompactDaily call.
type CompactDailyInput struct {
	// Collection is the raw top-level Parquet partition (e.g. "canonicals").
	Collection string
}

// CompactDailyResult summarises what CompactDaily did.
type CompactDailyResult struct {
	RowsBefore int
	RowsAfter  int
	Buckets    int
}

// CompactDaily reads every raw Parquet file across all dt= sub-prefixes
// for the given collection, reduces to one row per business key
// (latest OccurredAt wins), and writes one file per key-prefix bucket
// into the corresponding *_current/ partition.
func (c *Compactor) CompactDaily(ctx context.Context, in CompactDailyInput) (CompactDailyResult, error) {
	switch in.Collection {
	case "canonicals":
		return compactDailyGeneric[eventsv1.CanonicalUpsertedV1](ctx, c,
			"canonicals", "canonicals_current",
			func(r eventsv1.CanonicalUpsertedV1) (string, string, time.Time) {
				return r.ClusterID, "cc=" + first2(r.ClusterID), r.OccurredAt.UTC()
			})
	case "embeddings":
		return compactDailyGeneric[eventsv1.EmbeddingV1](ctx, c,
			"embeddings", "embeddings_current",
			func(r eventsv1.EmbeddingV1) (string, string, time.Time) {
				return r.CanonicalID, "cc=" + first2(r.CanonicalID), r.OccurredAt.UTC()
			})
	case "translations":
		return compactDailyGeneric[eventsv1.TranslationV1](ctx, c,
			"translations", "translations_current",
			func(r eventsv1.TranslationV1) (string, string, time.Time) {
				return r.CanonicalID + "|" + r.Lang,
					"cc=" + first2(r.CanonicalID) + "/lang=" + r.Lang,
					r.OccurredAt.UTC()
			})
	case "candidates_cv":
		return compactDailyGeneric[eventsv1.CVExtractedV1](ctx, c,
			"candidates_cv", "candidates_cv_current",
			func(r eventsv1.CVExtractedV1) (string, string, time.Time) {
				return r.CandidateID, "cnd=" + first2(r.CandidateID), r.OccurredAt.UTC()
			})
	case "candidates_embeddings":
		return compactDailyGeneric[eventsv1.CandidateEmbeddingV1](ctx, c,
			"candidates_embeddings", "candidates_embeddings_current",
			func(r eventsv1.CandidateEmbeddingV1) (string, string, time.Time) {
				return r.CandidateID, "cnd=" + first2(r.CandidateID), r.OccurredAt.UTC()
			})
	case "candidates_preferences":
		return compactDailyGeneric[eventsv1.PreferencesUpdatedV1](ctx, c,
			"candidates_preferences", "candidates_preferences_current",
			func(r eventsv1.PreferencesUpdatedV1) (string, string, time.Time) {
				return r.CandidateID, "cnd=" + first2(r.CandidateID), r.OccurredAt.UTC()
			})
	default:
		return CompactDailyResult{}, fmt.Errorf("compact daily: %q has no _current partition", in.Collection)
	}
}

// compactDailyGeneric is the generic implementation shared by all daily-compaction cases.
// It lists all objects under rawCollection/, reduces to one row per business key
// (latest OccurredAt wins), deletes the old _current/ partition, and writes one
// file per bucket.
func compactDailyGeneric[T any](
	ctx context.Context,
	c *Compactor,
	rawCollection, currentCollection string,
	bf func(T) (bkey string, bucket string, occ time.Time),
) (CompactDailyResult, error) {
	rawPrefix := rawCollection + "/"

	var keys []string
	cursor := ""
	for {
		page, err := c.reader.ListNewObjects(ctx, rawPrefix, cursor, 1000)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: list %q: %w", rawPrefix, err)
		}
		if len(page) == 0 {
			break
		}
		for _, o := range page {
			if o.Key != nil {
				keys = append(keys, *o.Key)
			}
		}
		lastKey := ""
		for i := len(page) - 1; i >= 0; i-- {
			if page[i].Key != nil {
				lastKey = *page[i].Key
				break
			}
		}
		if lastKey == "" {
			break
		}
		cursor = lastKey
	}
	if len(keys) == 0 {
		return CompactDailyResult{}, nil
	}

	latest := map[string]T{}
	latestAt := map[string]time.Time{}
	buckets := map[string][]string{}

	var rowsBefore int
	for _, k := range keys {
		body, err := c.reader.Get(ctx, k)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: get %q: %w", k, err)
		}
		rows, err := eventlog.ReadParquet[T](body)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: read %q: %w", k, err)
		}
		rowsBefore += len(rows)
		for _, r := range rows {
			bkey, bucket, occ := bf(r)
			if bkey == "" {
				continue
			}
			if prev, ok := latestAt[bkey]; !ok || occ.After(prev) {
				latest[bkey] = r
				latestAt[bkey] = occ
				buckets[bucket] = appendUnique(buckets[bucket], bkey)
			}
		}
	}

	if err := c.deletePrefix(ctx, currentCollection+"/"); err != nil {
		return CompactDailyResult{}, err
	}

	for bucket, bkeys := range buckets {
		sort.Strings(bkeys)
		out := make([]T, 0, len(bkeys))
		for _, k := range bkeys {
			out = append(out, latest[k])
		}
		body, err := eventlog.WriteParquet(out)
		if err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: write parquet: %w", err)
		}
		key := path.Join(currentCollection, bucket, "current-"+xid.New().String()+".parquet")
		if _, err := c.uploader.Put(ctx, key, body); err != nil {
			return CompactDailyResult{}, fmt.Errorf("compact daily: upload %q: %w", key, err)
		}
	}

	return CompactDailyResult{
		RowsBefore: rowsBefore,
		RowsAfter:  len(latest),
		Buckets:    len(buckets),
	}, nil
}

// deletePrefix removes all objects whose key starts with prefix.
func (c *Compactor) deletePrefix(ctx context.Context, prefix string) error {
	cursor := ""
	for {
		page, err := c.reader.ListNewObjects(ctx, prefix, cursor, 1000)
		if err != nil {
			return fmt.Errorf("compact daily: list for delete %q: %w", prefix, err)
		}
		if len(page) == 0 {
			return nil
		}
		for _, o := range page {
			if o.Key == nil {
				continue
			}
			_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(c.bucket),
				Key:    o.Key,
			})
			if err != nil {
				return fmt.Errorf("compact daily: delete %q: %w", *o.Key, err)
			}
		}
		lastKey := ""
		for i := len(page) - 1; i >= 0; i-- {
			if page[i].Key != nil {
				lastKey = *page[i].Key
				break
			}
		}
		if lastKey == "" {
			return nil
		}
		cursor = lastKey
	}
}

// first2 returns the lower-cased first two characters of s, or "xx" if s is
// shorter than two characters.
func first2(s string) string {
	if len(s) >= 2 {
		return strings.ToLower(s[:2])
	}
	return "xx"
}

// appendUnique appends v to s only if v is not already present.
func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

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
