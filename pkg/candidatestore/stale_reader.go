package candidatestore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// StaleCandidate is the output shape for stale-nudge consumers.
type StaleCandidate struct {
	CandidateID  string
	LastUploadAt time.Time
}

// StaleReader scans candidates_cv_current/ for candidates whose latest
// CV upload occurred_at is older than cutoff.
type StaleReader struct {
	reader *eventlog.Reader
}

// NewStaleReader constructs a reader bound to the shared R2 client.
func NewStaleReader(cli *s3.Client, bucket string) *StaleReader {
	return &StaleReader{reader: eventlog.NewReader(cli, bucket)}
}

// ListStale enumerates candidates whose most-recent CV upload is older
// than cutoff. Returns up to `limit` rows sorted ascending by
// LastUploadAt so the oldest get nudged first.
func (r *StaleReader) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]StaleCandidate, error) {
	if limit <= 0 {
		limit = 1000
	}

	seen := map[string]time.Time{}

	cursor := ""
	for {
		page, err := r.reader.ListNewObjects(ctx, "candidates_cv_current/", cursor, 1000)
		if err != nil {
			return nil, fmt.Errorf("stale reader: list: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, o := range page {
			if o.Key == nil {
				continue
			}
			body, err := r.reader.Get(ctx, *o.Key)
			if err != nil {
				return nil, fmt.Errorf("stale reader: get %q: %w", *o.Key, err)
			}
			rows, err := eventlog.ReadParquet[eventsv1.CVExtractedV1](body)
			if err != nil {
				return nil, fmt.Errorf("stale reader: parquet %q: %w", *o.Key, err)
			}
			for _, row := range rows {
				if row.CandidateID == "" {
					continue
				}
				occ := row.OccurredAt.UTC()
				if prev, ok := seen[row.CandidateID]; !ok || occ.After(prev) {
					seen[row.CandidateID] = occ
				}
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

	var out []StaleCandidate
	for id, at := range seen {
		if at.Before(cutoff) {
			out = append(out, StaleCandidate{CandidateID: id, LastUploadAt: at})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUploadAt.Before(out[j].LastUploadAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
