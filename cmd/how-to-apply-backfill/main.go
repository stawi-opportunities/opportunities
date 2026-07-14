// Command how-to-apply-backfill re-peels opportunity descriptions via the
// same NVIDIA Build inference path used by crawler / matching / frontier
// (integrate.api.nvidia.com + meta/llama-3.1-8b-instruct).
//
// Steady-state crawl peels via crawlaccept.PeelAccepted when INFERENCE_* is
// set. Trustage also ticks POST /admin/how-to-apply/backfill. This CLI is
// for operator-driven bulk reprocess and dry-runs.
//
// Usage:
//
//	DATABASE_URL=postgres://... \
//	INFERENCE_API_KEY=nvapi-... \
//	go run ./cmd/how-to-apply-backfill -concurrency 8
//
// When INFERENCE_BASE_URL / INFERENCE_MODEL are unset, defaults to NVIDIA
// Build (extraction.NVIDIABuildBaseURL / NVIDIABuildChatModel).
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/howtoapply"
)

func main() {
	concurrency := flag.Int("concurrency", 6, "parallel peel requests")
	batch := flag.Int("batch", 200, "rows fetched per outer loop")
	force := flag.Bool("force", false, "re-peel rows that already have how_to_apply")
	dryRun := flag.Bool("dry-run", false, "print actions without writing")
	minRunes := flag.Int("min-runes", 80, "skip descriptions shorter than this")
	flag.Parse()

	infURL, infModel, infKey := extraction.ResolveInferenceOrNVIDIA(
		os.Getenv("INFERENCE_BASE_URL"),
		os.Getenv("INFERENCE_MODEL"),
		os.Getenv("INFERENCE_API_KEY"),
	)
	if infURL == "" || infModel == "" || infKey == "" {
		fmt.Fprintln(os.Stderr, "how-to-apply-backfill: need INFERENCE_API_KEY (and optionally INFERENCE_BASE_URL / INFERENCE_MODEL)")
		fmt.Fprintf(os.Stderr, "  defaults: %s + %s (NVIDIA Build)\n", extraction.NVIDIABuildBaseURL, extraction.NVIDIABuildChatModel)
		os.Exit(2)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(*concurrency + 2)
	if err := db.PingContext(ctx); err != nil {
		fatalf("ping db: %v", err)
	}

	timeout := 120 * time.Second
	if v := os.Getenv("INFERENCE_TIMEOUT_SEC"); v != "" {
		if n, e := time.ParseDuration(v + "s"); e == nil && n > 0 {
			timeout = n
		}
	}
	ex := extraction.New(extraction.Config{
		BaseURL:    infURL,
		APIKey:     infKey,
		Model:      infModel,
		HTTPClient: &http.Client{Timeout: timeout},
	})
	fmt.Printf("how-to-apply-backfill: nvidia_build url=%s model=%s force=%v dry_run=%v concurrency=%d\n",
		infURL, infModel, *force, *dryRun, *concurrency)

	if err := howtoapply.EnsureColumn(ctx, db); err != nil {
		fatalf("ensure column: %v", err)
	}

	var done, peeled, skipped, failed int64
	start := time.Now()
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				fmt.Printf("  [hb] done=%d peeled=%d skipped=%d failed=%d (%.1f/s)\n",
					atomic.LoadInt64(&done), atomic.LoadInt64(&peeled),
					atomic.LoadInt64(&skipped), atomic.LoadInt64(&failed),
					float64(atomic.LoadInt64(&done))/time.Since(start).Seconds())
			}
		}
	}()

	for ctx.Err() == nil {
		rows, err := howtoapply.FetchBatch(ctx, db, *batch, *force, *minRunes)
		if err != nil {
			fmt.Printf("  fetch failed (%v); retrying in 3s\n", err)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
			continue
		}
		if len(rows) == 0 {
			break
		}
		sem := make(chan struct{}, *concurrency)
		var wg sync.WaitGroup
		for _, r := range rows {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(r howtoapply.Row) {
				defer wg.Done()
				defer func() { <-sem }()
				did, err := howtoapply.ProcessOne(ctx, db, ex, r, *force, *dryRun, *minRunes)
				atomic.AddInt64(&done, 1)
				if err != nil {
					atomic.AddInt64(&failed, 1)
					if atomic.LoadInt64(&failed) <= 8 {
						fmt.Printf("  [err] %s: %v\n", r.CanonicalID, err)
					}
					return
				}
				if did {
					atomic.AddInt64(&peeled, 1)
				} else {
					atomic.AddInt64(&skipped, 1)
				}
			}(r)
		}
		wg.Wait()
	}

	fmt.Printf("how-to-apply-backfill: done done=%d peeled=%d skipped=%d failed=%d elapsed=%s\n",
		atomic.LoadInt64(&done), atomic.LoadInt64(&peeled), atomic.LoadInt64(&skipped),
		atomic.LoadInt64(&failed), time.Since(start).Round(time.Second))
	if failed > 0 {
		os.Exit(1)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "how-to-apply-backfill: "+format+"\n", args...)
	os.Exit(1)
}
