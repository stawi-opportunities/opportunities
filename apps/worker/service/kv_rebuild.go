package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/util"
	"github.com/redis/go-redis/v9"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/kv"
	"github.com/stawi-opportunities/opportunities/pkg/memconfig"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// KVRebuilder repopulates Valkey cluster:* keys by scanning every registered
// kind's URL prefix in the R2 content bucket (e.g. jobs/, scholarships/) and
// applying the Lua CAS script to ensure the latest value wins even under
// parallel page fetches.
//
// Memory model (O(batch), not O(partition)):
//
//   - A memconfig.Budget("kv-rebuild", 30) governs maximum in-flight keys.
//   - When the bounded map reaches maxKeysInMemory, ALL current entries are
//     flushed to Valkey via the Lua CAS script.
//   - After a flush the map is reset; slug files that arrive later for the
//     same cluster_id are compared by the CAS script.
//
// Peak memory = maxKeysInMemory × 256 bytes, regardless of bucket size.
//
// Valkey key format:
//
//	SET cluster:<id> <json>
//
// Where <json> is the JSON-encoded kv.ClusterSnapshot, matching the
// format Frame's cache.Cache[string, kv.ClusterSnapshot] writes during
// normal pipeline operation.
type KVRebuilder struct {
	s3Client *s3.Client
	bucket   string
	kv       *redis.Client
	registry *opportunity.Registry
}

// NewKVRebuilder constructs a KVRebuilder backed by an S3-compatible client,
// a Valkey client, and the opportunity-kinds registry. The registry drives
// which R2 prefixes are walked on each rebuild run.
func NewKVRebuilder(s3Client *s3.Client, bucket string, kv *redis.Client, registry *opportunity.Registry) *KVRebuilder {
	return &KVRebuilder{s3Client: s3Client, bucket: bucket, kv: kv, registry: registry}
}

// KVRebuildResult holds counters reported by a single rebuild run.
type KVRebuildResult struct {
	Files          int `json:"files"`
	ClusterKeysSet int `json:"cluster_keys_set"`
}

// kvCASScript sets cluster:<id> to ARGV[1] ONLY if the new row's
// last_seen_at (ARGV[2], RFC3339 string) is strictly greater than the
// existing value's last_seen_at field. If the key is missing, sets
// unconditionally. Works against Frame's plain-string cluster cache
// format (GET returns the JSON-encoded kv.ClusterSnapshot body).
const kvCASScript = `
local existing = redis.call('GET', KEYS[1])
if existing == false or existing == nil then
    redis.call('SET', KEYS[1], ARGV[1])
    return 1
end
local existingTs = string.match(existing, '"last_seen_at":"([^"]+)"')
if existingTs == nil or ARGV[2] > existingTs then
    redis.call('SET', KEYS[1], ARGV[1])
    return 1
end
return 0
`

// Run is the rebuild entry point. It iterates every registered kind and
// walks <kind.URLPrefix>/ in R2, processing each slug-direct JSON.
func (r *KVRebuilder) Run(ctx context.Context) (KVRebuildResult, error) {
	var res KVRebuildResult
	if r.registry == nil {
		return res, fmt.Errorf("kv rebuild: registry is nil")
	}

	kinds := r.registry.Known()
	util.Log(ctx).
		WithField("bucket", r.bucket).
		WithField("kinds", kinds).
		Info("kv rebuild: starting registry-driven R2 scan")

	for _, kind := range kinds {
		spec := r.registry.Resolve(kind)
		prefix := spec.URLPrefix + "/"
		if err := r.walkPrefix(ctx, prefix, &res); err != nil {
			return res, fmt.Errorf("kv rebuild: kind %q: %w", kind, err)
		}
	}

	util.Log(ctx).
		WithField("files", res.Files).
		WithField("cluster_keys", res.ClusterKeysSet).
		Info("kv rebuild complete")
	return res, nil
}

// walkPrefix lists all R2 <prefix>*.json slug files, fetches them concurrently
// via a worker pool, folds per cluster_id keeping the latest last_seen_at, and
// writes Valkey via Lua CAS. Pages through the bucket up to 1000 objects at a
// time; concurrent GETs are bounded by the pool size. Counters accumulate into
// the supplied result so a multi-prefix Run reports a combined total.
func (r *KVRebuilder) walkPrefix(ctx context.Context, prefix string, res *KVRebuildResult) error {
	budget := memconfig.NewBudget("kv-rebuild", 30)
	maxKeysInMemory := budget.BatchSizeFor(256)
	util.Log(ctx).
		WithField("bucket", r.bucket).
		WithField("prefix", prefix).
		WithField("max_keys_in_memory", maxKeysInMemory).
		Info("kv rebuild: walking prefix")

	// Pool size: up to 16 concurrent GETs, bounded by budget.
	poolSize := 16
	if poolSize > maxKeysInMemory {
		poolSize = maxKeysInMemory
	}

	var (
		mu      sync.Mutex
		bounded = make(map[string]canonicalMinimal, maxKeysInMemory)
	)

	// flushIfNeeded checks the bounded map size and flushes if full.
	// Must be called with mu NOT held; acquires mu itself.
	flushIfNeeded := func(force bool) error {
		mu.Lock()
		need := force || len(bounded) >= maxKeysInMemory
		if !need {
			mu.Unlock()
			return nil
		}
		snapshot := bounded
		bounded = make(map[string]canonicalMinimal, maxKeysInMemory)
		mu.Unlock()

		flushed, err := r.flushToValkey(ctx, snapshot)
		if err != nil {
			return err
		}
		mu.Lock()
		res.ClusterKeysSet += flushed
		mu.Unlock()
		return nil
	}

	// workCh carries S3 object keys to fetch.
	workCh := make(chan string, poolSize*2)

	// errCh carries errors from the worker pool (first error wins).
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	// Start the worker pool.
	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range workCh {
				// Fetch slug JSON.
				out, err := r.s3Client.GetObject(ctx, &s3.GetObjectInput{
					Bucket: aws.String(r.bucket),
					Key:    aws.String(key),
				})
				if err != nil {
					select {
					case errCh <- fmt.Errorf("kv rebuild: GET %s: %w", key, err):
					default:
					}
					return
				}
				var c eventsv1.CanonicalUpsertedV1
				if decErr := json.NewDecoder(out.Body).Decode(&c); decErr != nil {
					_ = out.Body.Close()
					// Skip malformed files rather than aborting.
					util.Log(ctx).WithError(decErr).
						WithField("key", key).
						Warn("kv rebuild: skip malformed slug JSON")
					return
				}
				_ = out.Body.Close()

				if c.OpportunityID == "" {
					return
				}

				row := canonicalMinimalFromCanonical(c)
				mu.Lock()
				existing, ok := bounded[c.OpportunityID]
				if !ok || row.OccurredAt.After(existing.OccurredAt) {
					bounded[c.OpportunityID] = row
				}
				mu.Unlock()

				if flushErr := flushIfNeeded(false); flushErr != nil {
					select {
					case errCh <- flushErr:
					default:
					}
					return
				}
			}
		}()
	}

	// Page through the prefix and send keys to the pool.
	var continuationToken *string
	for {
		input := &s3.ListObjectsV2Input{
			Bucket:  aws.String(r.bucket),
			Prefix:  aws.String(prefix),
			MaxKeys: aws.Int32(1000),
		}
		if continuationToken != nil {
			input.ContinuationToken = continuationToken
		}

		page, err := r.s3Client.ListObjectsV2(ctx, input)
		if err != nil {
			close(workCh)
			wg.Wait()
			return fmt.Errorf("list R2: %w", err)
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			// Only top-level slug files: <prefix><slug>.json (one slash total).
			if !strings.HasSuffix(key, ".json") {
				continue
			}
			// Skip translation files: <prefix><slug>/<lang>.json
			// They have two slashes total; slug files have one slash.
			if strings.Count(key, "/") != 1 {
				continue
			}

			mu.Lock()
			res.Files++
			mu.Unlock()

			select {
			case err := <-errCh:
				close(workCh)
				wg.Wait()
				return err
			case workCh <- key:
			}
		}

		if !aws.ToBool(page.IsTruncated) {
			break
		}
		continuationToken = page.NextContinuationToken
	}

	close(workCh)
	wg.Wait()

	// Drain errCh in case a worker error fired at the same time we closed.
	select {
	case err := <-errCh:
		return err
	default:
	}

	// Final flush of remaining entries.
	if err := flushIfNeeded(true); err != nil {
		return err
	}

	return nil
}

// flushToValkey writes each (clusterID → canonicalMinimal) entry to
// Valkey via pipelined Lua CAS.
func (r *KVRebuilder) flushToValkey(ctx context.Context, m map[string]canonicalMinimal) (int, error) {
	if len(m) == 0 {
		return 0, nil
	}

	pipe := r.kv.Pipeline()
	batchSize := 0
	keysWritten := 0

	for clusterID, row := range m {
		snap := clusterSnapshotFromMinimal(row)
		body, err := json.Marshal(snap)
		if err != nil {
			continue // skip corrupt row
		}
		tsISO := snap.LastSeenAt.UTC().Format(time.RFC3339)
		key := "cluster:" + clusterID

		pipe.Eval(ctx, kvCASScript, []string{key}, string(body), tsISO)
		keysWritten++
		batchSize++

		if batchSize >= 500 {
			if _, err := pipe.Exec(ctx); err != nil {
				return keysWritten - batchSize, fmt.Errorf("pipe exec: %w", err)
			}
			pipe = r.kv.Pipeline()
			batchSize = 0
		}
	}
	if batchSize > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return keysWritten - batchSize, fmt.Errorf("pipe final: %w", err)
		}
	}

	return keysWritten, nil
}

// canonicalMinimalFromCanonical converts a CanonicalUpsertedV1 (from a slug
// JSON file) to the canonicalMinimal struct used by the bounded map.
//
// Attributes is carried verbatim — the slug JSON stores per-kind facets
// (employment_type/seniority for jobs, field_of_study/degree_level for
// scholarships, etc.) inside Attributes; the merge handler reads them
// back from ClusterSnapshot.Attributes on the hot path.
func canonicalMinimalFromCanonical(c eventsv1.CanonicalUpsertedV1) canonicalMinimal {
	lang, _ := c.Attributes["language"].(string)
	remote, _ := c.Attributes["remote_type"].(string)
	category := ""
	if len(c.Categories) > 0 {
		category = c.Categories[0]
	}
	return canonicalMinimal{
		ClusterID:    c.OpportunityID,
		CanonicalID:  c.OpportunityID,
		Slug:         c.Slug,
		Kind:         c.Kind,
		Title:        c.Title,
		Company:      c.IssuingEntity,
		Country:      c.AnchorCountry,
		Language:     lang,
		RemoteType:   remote,
		SalaryMin:    c.AmountMin,
		SalaryMax:    c.AmountMax,
		Currency:     c.Currency,
		Category:     category,
		QualityScore: 0,
		Status:       "active",
		FirstSeenAt:  c.UpsertedAt,
		LastSeenAt:   c.UpsertedAt,
		PostedAt:     c.PostedAt,
		ApplyURL:     c.ApplyURL,
		OccurredAt:   c.UpsertedAt,
		Attributes:   c.Attributes,
	}
}

// canonicalMinimal carries the subset of canonical fields needed for KV
// rebuild. OccurredAt is used for the in-Go fold (latest-per-cluster_id).
type canonicalMinimal struct {
	ClusterID    string
	CanonicalID  string
	Slug         string
	Kind         string
	Title        string
	Company      string
	Country      string
	Language     string
	RemoteType   string
	SalaryMin    float64
	SalaryMax    float64
	Currency     string
	Category     string
	QualityScore float64
	Status       string
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	PostedAt     time.Time
	OccurredAt   time.Time
	ApplyURL     string
	Attributes   map[string]any
}

// clusterSnapshotFromMinimal maps a canonicalMinimal to the kv.ClusterSnapshot
// shape that the canonical-merge handler reads on the hot path.
func clusterSnapshotFromMinimal(row canonicalMinimal) kv.ClusterSnapshot {
	return kv.ClusterSnapshot{
		ClusterID:    row.ClusterID,
		CanonicalID:  row.CanonicalID,
		Slug:         row.Slug,
		Kind:         row.Kind,
		Title:        row.Title,
		Company:      row.Company,
		Country:      row.Country,
		Language:     row.Language,
		RemoteType:   row.RemoteType,
		SalaryMin:    row.SalaryMin,
		SalaryMax:    row.SalaryMax,
		Currency:     row.Currency,
		Category:     row.Category,
		QualityScore: row.QualityScore,
		Status:       row.Status,
		FirstSeenAt:  row.FirstSeenAt,
		LastSeenAt:   row.LastSeenAt,
		PostedAt:     row.PostedAt,
		ApplyURL:     row.ApplyURL,
		Attributes:   row.Attributes,
	}
}
