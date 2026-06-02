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
// It is idempotent and resumable: it only touches rows WHERE embedding
// IS NULL, so re-running after an interruption picks up exactly what's
// left. It reuses extraction.EmbedInput so the vectors it produces are
// byte-for-byte comparable with the live handler's.
//
// Usage (point EMBEDDING_BASE_URL/DATABASE_URL at port-forwarded
// services, or run in-cluster):
//
//	DATABASE_URL=postgres://opportunities:...@localhost:5432/opportunities?sslmode=disable \
//	EMBEDDING_BASE_URL=http://localhost:8080 \
//	EMBEDDING_MODEL=intfloat/multilingual-e5-small \
//	go run ./cmd/embed-backfill -concurrency 12
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ex := extraction.New(extraction.Config{
		EmbeddingBaseURL: embedURL,
		EmbeddingAPIKey:  os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:   embedModel,
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

	var total int
	_ = db.QueryRowContext(ctx,
		`SELECT count(*) FROM opportunities WHERE embedding IS NULL AND status='active' AND hidden=false`,
	).Scan(&total)
	fmt.Printf("embed-backfill: %d active opportunities with NULL embedding; concurrency=%d\n", total, *concurrency)

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

	for ctx.Err() == nil {
		rows, err := fetchBatch(ctx, db, *batch)
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
			break // no NULL rows left
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
				if err := embedOne(ctx, db, ex, r); err != nil {
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
	}

	fmt.Printf("embed-backfill: done. embedded=%d failed=%d elapsed=%s\n",
		atomic.LoadInt64(&done), atomic.LoadInt64(&failed), time.Since(start).Round(time.Second))
	if failed > 0 {
		os.Exit(1)
	}
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
}

func fetchBatch(ctx context.Context, db *sql.DB, n int) ([]oppRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT canonical_id,
		       COALESCE(title,''),
		       COALESCE(issuing_entity,''),
		       COALESCE(attributes->>'description','')
		FROM opportunities
		WHERE embedding IS NULL AND status='active' AND hidden=false
		ORDER BY first_seen_at DESC
		LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []oppRow
	for rows.Next() {
		var r oppRow
		if err := rows.Scan(&r.canonicalID, &r.title, &r.issuingEntity, &r.description); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func embedOne(ctx context.Context, db *sql.DB, ex *extraction.Extractor, r oppRow) error {
	text := extraction.EmbedInput(r.title, r.issuingEntity, r.description)
	vec, err := ex.Embed(ctx, text)
	if err != nil {
		return err
	}
	if len(vec) == 0 {
		return nil // embedder disabled — nothing to write
	}
	lit := vectorLiteral(vec)
	// Retry the write across transient connectivity blips (e.g. a
	// port-forward reset) so a momentary outage doesn't waste the embed
	// we already paid for. Idempotent: the WHERE embedding IS NULL guard
	// makes a re-applied update a no-op.
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		_, lastErr = db.ExecContext(ctx,
			`UPDATE opportunities SET embedding = $1::vector, updated_at = now()
			 WHERE canonical_id = $2 AND embedding IS NULL`,
			lit, r.canonicalID)
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
