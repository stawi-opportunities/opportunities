package definitions

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/util"
)

// R2Config carries the R2 connection params + the bucket + prefix
// for definitions storage. The S3 client is constructed by the
// caller — typically reusing the same client the archive package
// already wires.
type R2Config struct {
	// Client is a pre-constructed S3 client pointed at R2 (or any
	// S3-compatible endpoint).
	Client *s3.Client
	// Bucket is the R2 bucket name (e.g. "product-opportunities-content").
	Bucket string
	// Prefix is the object-key prefix under which definitions live.
	// Defaults to "definitions".
	Prefix string
	// RefreshInterval is how often Start's background loop re-lists
	// R2 to pick up admin edits. Defaults to 5 minutes.
	RefreshInterval time.Duration
}

// R2Loader is the production Loader. Backs definitions in R2 with a
// 5-min refresh ticker + NATS-driven instant invalidation. Construct
// once per process; share across handlers.
type R2Loader struct {
	cfg     R2Config
	cache   sync.Map // key: "type/name" → r2Entry
	subs    sync.Map // key: Type → []func(name, version string)
	subsMu  sync.Mutex
	stopCh  chan struct{}
	stopped sync.Once
}

type r2Entry struct {
	Entry
	body []byte
}

// NewR2Loader constructs a loader. Call Start(ctx) to populate the
// initial cache and begin the refresh ticker.
func NewR2Loader(cfg R2Config) *R2Loader {
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 5 * time.Minute
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "definitions"
	}
	cfg.Prefix = strings.TrimRight(cfg.Prefix, "/")
	return &R2Loader{cfg: cfg, stopCh: make(chan struct{})}
}

// Start populates the cache from R2 and kicks off the refresh loop.
// Blocks until the initial fetch completes; returns the error so the
// caller can decide whether to keep booting on a broken R2.
func (r *R2Loader) Start(ctx context.Context) error {
	if err := r.refresh(ctx); err != nil {
		return fmt.Errorf("definitions: initial R2 fetch: %w", err)
	}
	go r.refreshLoop(ctx)
	return nil
}

// Stop ends the background refresh. Safe to call multiple times.
func (r *R2Loader) Stop() {
	r.stopped.Do(func() { close(r.stopCh) })
}

func (r *R2Loader) refreshLoop(ctx context.Context) {
	t := time.NewTicker(r.cfg.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.refresh(ctx); err != nil {
				util.Log(ctx).WithError(err).Warn("definitions: refresh tick failed; serving stale cache")
			}
		}
	}
}

// refresh lists every definition object and re-fetches any whose
// ETag changed since the last refresh. Idempotent.
func (r *R2Loader) refresh(ctx context.Context) error {
	for _, t := range []Type{TypeKind, TypePrompt, TypeConnector, TypeSeed} {
		if err := r.refreshType(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

func (r *R2Loader) refreshType(ctx context.Context, t Type) error {
	prefix := r.cfg.Prefix + "/" + string(t) + "/"
	pages := s3.NewListObjectsV2Paginator(r.cfg.Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(r.cfg.Bucket),
		Prefix: aws.String(prefix),
	})
	seen := make(map[string]struct{})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			etag := strings.Trim(aws.ToString(obj.ETag), `"`)
			name := strings.TrimSuffix(path.Base(key), path.Ext(key)) // strip .yaml etc
			if name == "" {
				continue
			}
			seen[name] = struct{}{}
			cacheKey := string(t) + "/" + name
			// Skip re-fetch if etag matches.
			if existing, ok := r.cache.Load(cacheKey); ok {
				if existing.(r2Entry).Version == etag {
					continue
				}
			}
			body, err := r.fetch(ctx, key)
			if err != nil {
				util.Log(ctx).WithError(err).WithField("key", key).Warn("definitions: fetch failed")
				continue
			}
			entry := r2Entry{
				Entry: Entry{
					Type:      t,
					Name:      name,
					Version:   etag,
					UpdatedAt: aws.ToTime(obj.LastModified),
					Size:      aws.ToInt64(obj.Size),
				},
				body: body,
			}
			r.cache.Store(cacheKey, entry)
			r.fireSubs(t, name, etag)
		}
	}
	// Detect deletions: any cached entry of this type whose name
	// isn't in `seen` was deleted upstream.
	r.cache.Range(func(k, v any) bool {
		key := k.(string)
		if !strings.HasPrefix(key, string(t)+"/") {
			return true
		}
		name := strings.TrimPrefix(key, string(t)+"/")
		if _, present := seen[name]; !present {
			r.cache.Delete(key)
			r.fireSubs(t, name, "deleted")
		}
		return true
	})
	return nil
}

func (r *R2Loader) fetch(ctx context.Context, key string) ([]byte, error) {
	out, err := r.cfg.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Body.Close() }()
	return io.ReadAll(out.Body)
}

// Get returns the cached body + version for (type, name). Stale-cache
// reads are deliberate — the loader prioritises availability over
// freshness on R2 outage.
func (r *R2Loader) Get(_ context.Context, t Type, name string) ([]byte, string, error) {
	v, ok := r.cache.Load(string(t) + "/" + name)
	if !ok {
		return nil, "", ErrNotFound
	}
	e := v.(r2Entry)
	return e.body, e.Version, nil
}

// List returns every cached entry of a type, sorted by Name.
func (r *R2Loader) List(_ context.Context, t Type) ([]Entry, error) {
	var out []Entry
	r.cache.Range(func(k, v any) bool {
		if strings.HasPrefix(k.(string), string(t)+"/") {
			out = append(out, v.(r2Entry).Entry)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Subscribe registers a callback fired whenever a definition of type
// `t` changes (refresh tick OR Invalidate push). The returned func is
// best-effort unsubscribe — subscribers are typically per-process
// lifetime so a leak-tolerant impl is fine.
func (r *R2Loader) Subscribe(t Type, fn func(name, version string)) func() {
	r.subsMu.Lock()
	defer r.subsMu.Unlock()
	existing, _ := r.subs.LoadOrStore(t, []func(string, string){})
	list := append(existing.([]func(string, string)), fn)
	r.subs.Store(t, list)
	return func() { /* leak-tolerant; subscribers are per-process lifetime */ }
}

func (r *R2Loader) fireSubs(t Type, name, version string) {
	raw, ok := r.subs.Load(t)
	if !ok {
		return
	}
	for _, fn := range raw.([]func(string, string)) {
		fn(name, version)
	}
}

// Invalidate is called by the NATS consumer when admin edits land.
// Re-fetches the one object NOW; doesn't wait for the next refresh
// tick. On 404 (object deleted), evicts from cache + fires subscribers
// with version="deleted".
func (r *R2Loader) Invalidate(ctx context.Context, t Type, name string) error {
	key := r.cfg.Prefix + "/" + string(t) + "/" + name + ".yaml"
	body, err := r.fetch(ctx, key)
	if err != nil {
		// 404 — treat as delete.
		r.cache.Delete(string(t) + "/" + name)
		r.fireSubs(t, name, "deleted")
		return nil
	}
	// We don't have ETag here cheaply — use a timestamp-derived "version".
	version := time.Now().UTC().Format(time.RFC3339Nano)
	r.cache.Store(string(t)+"/"+name, r2Entry{
		Entry: Entry{
			Type:      t,
			Name:      name,
			Version:   version,
			UpdatedAt: time.Now().UTC(),
			Size:      int64(len(body)),
		},
		body: body,
	})
	r.fireSubs(t, name, version)
	return nil
}
