// Package candidatestore provides read-only access to the latest
// per-candidate derived state (embedding, preferences) persisted as
// Parquet in R2. The match endpoint uses it to avoid reading Postgres
// on the hot path.
//
// Writes are never performed here — the writer service is the only
// producer of candidates_*_current/ Parquet files, via compaction of
// the daily partitions.
package candidatestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// ErrNotFound is returned when no embedding/preferences row exists
// for the requested candidate.
var ErrNotFound = errors.New("candidatestore: candidate state not found")

// Reader wraps an R2 client + bucket and fetches the latest typed row
// per candidate from the *_current/ partitions.
type Reader struct {
	client *s3.Client
	bucket string
}

// NewReader builds a Reader.
func NewReader(client *s3.Client, bucket string) *Reader {
	return &Reader{client: client, bucket: bucket}
}

// LatestEmbedding returns the highest-CVVersion embedding row for the
// candidate, or ErrNotFound if none exist.
func (r *Reader) LatestEmbedding(ctx context.Context, candidateID string) (eventsv1.CandidateEmbeddingV1, error) {
	rows, err := loadAll[eventsv1.CandidateEmbeddingV1](ctx, r.client, r.bucket,
		"candidates_embeddings_current/cnd="+prefix2(candidateID)+"/")
	if err != nil {
		return eventsv1.CandidateEmbeddingV1{}, err
	}
	best, ok := pick(rows,
		func(row eventsv1.CandidateEmbeddingV1) bool { return row.CandidateID == candidateID },
		func(a, b eventsv1.CandidateEmbeddingV1) bool { return a.CVVersion > b.CVVersion },
	)
	if !ok {
		return eventsv1.CandidateEmbeddingV1{}, ErrNotFound
	}
	return best, nil
}

// LatestPreferences returns the most recent preferences row for the
// candidate, or ErrNotFound if none exist. Preferences don't carry a
// CVVersion (they are independent of the CV cycle); ordering is by
// discovered object-key order which matches write order at this scale.
func (r *Reader) LatestPreferences(ctx context.Context, candidateID string) (eventsv1.PreferencesUpdatedV1, error) {
	rows, err := loadAll[eventsv1.PreferencesUpdatedV1](ctx, r.client, r.bucket,
		"candidates_preferences_current/cnd="+prefix2(candidateID)+"/")
	if err != nil {
		return eventsv1.PreferencesUpdatedV1{}, err
	}
	matching := filter(rows, func(row eventsv1.PreferencesUpdatedV1) bool { return row.CandidateID == candidateID })
	if len(matching) == 0 {
		return eventsv1.PreferencesUpdatedV1{}, ErrNotFound
	}
	return matching[len(matching)-1], nil
}

// prefix2 returns the two-char hex partition prefix for a candidateID.
// CandidateIDs are prefixed with a type tag (e.g. "cnd_") followed by
// the opaque xid. We strip the leading "cnd_" prefix (if present) and
// return the first two lowercased chars of the remainder. This aligns
// with the `candidates_*_current/cnd=<prefix>/` partition layout where
// <prefix> is the first two chars of the raw candidate xid.
func prefix2(candidateID string) string {
	const typePrefix = "cnd_"
	id := candidateID
	if len(id) > len(typePrefix) && id[:len(typePrefix)] == typePrefix {
		id = id[len(typePrefix):]
	}
	if len(id) >= 2 {
		return lowerASCII(id[:2])
	}
	return lowerASCII(id)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// loadAll lists Parquet objects under prefix and decodes them as []T.
// Uses eventlog.ReadParquet which wraps parquet.NewGenericReader[T].
func loadAll[T any](ctx context.Context, client *s3.Client, bucket, prefix string) ([]T, error) {
	keys, err := listKeys(ctx, client, bucket, prefix)
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)

	var rows []T
	for _, k := range keys {
		obj, err := client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(k),
		})
		if err != nil {
			return nil, fmt.Errorf("candidatestore: get %q: %w", k, err)
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, obj.Body); err != nil {
			_ = obj.Body.Close()
			return nil, fmt.Errorf("candidatestore: read %q: %w", k, err)
		}
		_ = obj.Body.Close()
		body := buf.Bytes()
		page, err := eventlog.ReadParquet[T](body)
		if err != nil {
			return nil, fmt.Errorf("candidatestore: decode %q: %w", k, err)
		}
		rows = append(rows, page...)
	}
	return rows, nil
}

// listKeys pages through ListObjectsV2 and returns every key under prefix.
func listKeys(ctx context.Context, client *s3.Client, bucket, prefix string) ([]string, error) {
	var keys []string
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("candidatestore: list %q: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return keys, nil
}

func filter[T any](xs []T, keep func(T) bool) []T {
	var out []T
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

func pick[T any](xs []T, keep func(T) bool, better func(a, b T) bool) (T, bool) {
	var zero T
	first := true
	var best T
	for _, x := range xs {
		if !keep(x) {
			continue
		}
		if first || better(x, best) {
			best = x
			first = false
		}
	}
	if first {
		return zero, false
	}
	return best, true
}
