// Command embed-backfill re-derives the embedding vector for every active
// opportunity whose embedding column is NULL and writes it back.
//
// Why this exists: the steady-state embed path only fires on new
// CanonicalUpsertedV1 events, and the bounded-retry embed worker
// ack-drops a canonical after maxEmbedAttempts. So a corpus that was
// crawled while embeddings were broken (e.g. the vector(1024) vs 384-dim
// model mismatch that left all 19k rows NULL, or a TEI outage that
// exhausted the retry budget) never self-heals — those rows stay NULL
// forever. This tool fills the gap deterministically.
//
// It is idempotent and resumable: by default it only touches rows WHERE
// embedding IS NULL. Pass -force to re-embed every active opportunity
// (required after switching embed models, e.g. e5-v5 → multilingual
// llama-nemotron). Reuses extraction.EmbedInput so vectors match the
// live worker handler.
//
// Usage (point EMBEDDING_BASE_URL/DATABASE_URL at port-forwarded
// services, or run in-cluster):
//
//	DATABASE_URL=postgres://opportunities:...@localhost:5432/opportunities?sslmode=disable \
//	EMBEDDING_BASE_URL=https://integrate.api.nvidia.com \
//	EMBEDDING_MODEL=nvidia/llama-nemotron-embed-1b-v2 \
//	EMBEDDING_DIMENSIONS=1024 \
//	EMBEDDING_API_KEY=nvapi-... \
//	go run ./cmd/embed-backfill -concurrency 12 -force
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	// pgx in simple-query mode is compatible with pgbouncer transaction
	// pooling (the pooler-rw service), which rejects the extended-protocol
	// prepared statements lib/pq emits ("unnamed prepared statement does
	// not exist"). The pooler is also far more tolerant of connection
	// churn than a direct port-forward to a single DB pod.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

func main() {
	concurrency := flag.Int("concurrency", 12, "parallel embed requests (keep <= TEI max-concurrent-requests * replicas)")
	batch := flag.Int("batch", 500, "rows fetched per round")
	force := flag.Bool("force", false, "re-embed all active opportunities (model migration); default only NULL embeddings")
	stdio := flag.Bool("stdio", false, "stdio mode: read CSV (canonical_id,text) from stdin, "+
		"write CSV (canonical_id,vector_literal) to stdout, no DB. Use when a direct DB "+
		"connection is unavailable (drive DB I/O via `kubectl exec psql` COPY instead).")
	flag.Parse()

	embedURL := os.Getenv("EMBEDDING_BASE_URL")
	embedModel := os.Getenv("EMBEDDING_MODEL")
	if embedURL == "" {
		fmt.Fprintln(os.Stderr, "EMBEDDING_BASE_URL is required")
		os.Exit(2)
	}
	// Pin output width to the pgvector column for Matryoshka models
	// (llama-nemotron native 2048 → 1024) so the backfill writes
	// column-compatible vectors.
	embedDims, _ := strconv.Atoi(os.Getenv("EMBEDDING_DIMENSIONS"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ex := extraction.New(extraction.Config{
		EmbeddingBaseURL:    embedURL,
		EmbeddingAPIKey:     os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:      embedModel,
		EmbeddingDimensions: embedDims,
		EmbeddingInputType:  firstNonEmpty(os.Getenv("EMBEDDING_INPUT_TYPE"), "passage"),
	})

	if *stdio {
		if err := runStdio(ctx, ex, *concurrency); err != nil {
			fatalf("stdio: %v", err)
		}
		return
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required (or pass -stdio)")
		os.Exit(2)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(*concurrency + 2)
	if err := db.PingContext(ctx); err != nil {
		fatalf("ping db: %v", err)
	}

	countSQL := `SELECT count(*) FROM opportunities WHERE status='active' AND hidden=false`
	if !*force {
		countSQL += ` AND embedding IS NULL`
	}
	var total int
	_ = db.QueryRowContext(ctx, countSQL).Scan(&total)
	mode := "NULL embeddings only"
	if *force {
		mode = "FORCE re-embed all active"
	}
	fmt.Printf("embed-backfill: %d active opportunities (%s); concurrency=%d model=%s dims=%d\n",
		total, mode, *concurrency, embedModel, embedDims)

	var done, failed int64
	var sampled int64
	start := time.Now()

	// Periodic heartbeat so a stalled or failing run is visible immediately
	// instead of waiting for the every-200-successes progress line.
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				d, f := atomic.LoadInt64(&done), atomic.LoadInt64(&failed)
				fmt.Printf("  [hb] embedded=%d failed=%d (%.1f/s)\n", d, f, float64(d)/time.Since(start).Seconds())
			}
		}
	}()

	// Cursor for force mode: ORDER BY first_seen_at DESC, advance by last ID.
	var afterSeen time.Time
	var afterID string

	for ctx.Err() == nil {
		rows, err := fetchBatch(ctx, db, *batch, *force, afterSeen, afterID)
		if err != nil {
			// Transient connectivity (e.g. a port-forward blip) shouldn't
			// abort a multi-thousand-row run. Back off and retry — the work
			// is idempotent, so nothing is lost.
			fmt.Printf("  fetch batch failed (%v); retrying in 3s\n", err)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
			continue
		}
		if len(rows) == 0 {
			break // no rows left
		}

		sem := make(chan struct{}, *concurrency)
		var wg sync.WaitGroup
		for _, r := range rows {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(r oppRow) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := embedOne(ctx, db, ex, r, *force); err != nil {
					atomic.AddInt64(&failed, 1)
					if atomic.AddInt64(&sampled, 1) <= 5 {
						fmt.Printf("  [err] %s: %v\n", r.canonicalID, err)
					}
					return
				}
				n := atomic.AddInt64(&done, 1)
				if n%200 == 0 {
					rate := float64(n) / time.Since(start).Seconds()
					fmt.Printf("  embedded=%d failed=%d (%.1f/s)\n", n, atomic.LoadInt64(&failed), rate)
				}
			}(r)
		}
		wg.Wait()
		// Advance cursor for force mode (stable page by first_seen_at, id).
		last := rows[len(rows)-1]
		afterSeen = last.firstSeenAt
		afterID = last.canonicalID
	}

	fmt.Printf("embed-backfill: done. embedded=%d failed=%d elapsed=%s\n",
		atomic.LoadInt64(&done), atomic.LoadInt64(&failed), time.Since(start).Round(time.Second))
	if failed > 0 {
		os.Exit(1)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// runStdio reads CSV records (canonical_id,text) from stdin, embeds each
// text via TEI with the given concurrency, and writes CSV records
// (canonical_id,vector_literal) to stdout. It carries no DB dependency so
// the heavy embed traffic can run against a stable TEI port-forward while
// the (here unreliable) DB I/O is driven separately via `kubectl exec
// psql` COPY. Order is not preserved; the canonical_id key joins them back.
func runStdio(ctx context.Context, ex *extraction.Extractor, concurrency int) error {
	r := csv.NewReader(bufio.NewReaderSize(os.Stdin, 1<<20))
	r.FieldsPerRecord = 2
	r.LazyQuotes = true
	w := csv.NewWriter(bufio.NewWriterSize(os.Stdout, 1<<20))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex // serialises csv.Writer (not goroutine-safe)
	var done, failed int64

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read csv: %w", err)
		}
		if ctx.Err() != nil {
			break
		}
		id, text := rec[0], rec[1]
		wg.Add(1)
		sem <- struct{}{}
		go func(id, text string) {
			defer wg.Done()
			defer func() { <-sem }()
			vec, err := ex.Embed(ctx, text)
			if err != nil || len(vec) == 0 {
				atomic.AddInt64(&failed, 1)
				return
			}
			lit := vectorLiteral(vec)
			mu.Lock()
			_ = w.Write([]string{id, lit})
			mu.Unlock()
			if n := atomic.AddInt64(&done, 1); n%500 == 0 {
				fmt.Fprintf(os.Stderr, "  stdio embedded=%d failed=%d\n", n, atomic.LoadInt64(&failed))
			}
		}(id, text)
	}
	wg.Wait()
	w.Flush()
	fmt.Fprintf(os.Stderr, "stdio: done embedded=%d failed=%d\n",
		atomic.LoadInt64(&done), atomic.LoadInt64(&failed))
	return w.Error()
}

type oppRow struct {
	canonicalID   string
	title         string
	issuingEntity string
	description   string
	firstSeenAt   time.Time
}

func fetchBatch(ctx context.Context, db *sql.DB, n int, force bool, afterSeen time.Time, afterID string) ([]oppRow, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if force {
		// Keyset pagination so force mode can walk the whole active corpus.
		if afterID == "" {
			rows, err = db.QueryContext(ctx, `
				SELECT canonical_id,
				       COALESCE(title,''),
				       COALESCE(issuing_entity,''),
				       COALESCE(attributes->>'description',''),
				       first_seen_at
				FROM opportunities
				WHERE status='active' AND hidden=false
				ORDER BY first_seen_at DESC, canonical_id DESC
				LIMIT $1`, n)
		} else {
			rows, err = db.QueryContext(ctx, `
				SELECT canonical_id,
				       COALESCE(title,''),
				       COALESCE(issuing_entity,''),
				       COALESCE(attributes->>'description',''),
				       first_seen_at
				FROM opportunities
				WHERE status='active' AND hidden=false
				  AND (first_seen_at, canonical_id) < ($2::timestamptz, $3::text)
				ORDER BY first_seen_at DESC, canonical_id DESC
				LIMIT $1`, n, afterSeen, afterID)
		}
	} else {
		rows, err = db.QueryContext(ctx, `
			SELECT canonical_id,
			       COALESCE(title,''),
			       COALESCE(issuing_entity,''),
			       COALESCE(attributes->>'description',''),
			       first_seen_at
			FROM opportunities
			WHERE embedding IS NULL AND status='active' AND hidden=false
			ORDER BY first_seen_at DESC, canonical_id DESC
			LIMIT $1`, n)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []oppRow
	for rows.Next() {
		var r oppRow
		if err := rows.Scan(&r.canonicalID, &r.title, &r.issuingEntity, &r.description, &r.firstSeenAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func embedOne(ctx context.Context, db *sql.DB, ex *extraction.Extractor, r oppRow, force bool) error {
	text := extraction.EmbedInput(r.title, r.issuingEntity, r.description)
	vec, err := ex.Embed(ctx, text)
	if err != nil {
		return err
	}
	if len(vec) == 0 {
		return fmt.Errorf("empty embedding vector")
	}
	lit := vectorLiteral(vec)
	// Retry the write across transient connectivity blips (e.g. a
	// port-forward reset) so a momentary outage doesn't waste the embed
	// we already paid for.
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		if force {
			_, lastErr = db.ExecContext(ctx,
				`UPDATE opportunities SET embedding = $1::vector, updated_at = now()
				 WHERE canonical_id = $2`,
				lit, r.canonicalID)
		} else {
			// Idempotent: WHERE embedding IS NULL makes a re-applied update a no-op.
			_, lastErr = db.ExecContext(ctx,
				`UPDATE opportunities SET embedding = $1::vector, updated_at = now()
				 WHERE canonical_id = $2 AND embedding IS NULL`,
				lit, r.canonicalID)
		}
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// vectorLiteral renders a float32 slice as a pgvector text literal:
// "[0.1,0.2,...]". Mirrors pkg/variantstate.vectorLiteral.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "embed-backfill: "+format+"\n", args...)
	os.Exit(1)
}
