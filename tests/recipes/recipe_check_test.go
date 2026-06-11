// Package recipes pins every source's extraction recipe against recorded
// pages: `go test ./tests/recipes` re-verifies that each (source, recipe)
// pair still crawls correctly — list rule finds items, detail extraction
// fills the required fields, items pass opportunity.Verify — without
// touching the network, so it runs in CI on every commit.
//
// Fixture layout (one directory per source under fixtures/):
//
//	fixture.json   — {"source": {...domain.Source fields...},
//	                  "sample_urls": ["https://…/job/x", …]}
//	recipe.json    — the recipe under test (what production activates)
//	pages/         — recorded HTML/JSON bodies + pages.json manifest
//
// Refresh a fixture's pages from the LIVE site (requires network):
//
//	go test ./tests/recipes -run TestRecipeFixtures -update
//
// The -update run fetches through the real HTTP client, records every
// page the check touches, and then asserts exactly like the replay run —
// so recording a fixture IS verifying the recipe against the live source.
package recipes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stawi-opportunities/opportunities/pkg/recipe/recipecheck"
)

var update = flag.Bool("update", false, "re-record fixture pages from the live sites")

const passThreshold = 0.8

type fixtureMeta struct {
	Source     domain.Source `json:"source"`
	SampleURLs []string      `json:"sample_urls"`
	// Synthetic fixtures exercise the harness itself against committed
	// pages for a fictional board — there is no live site to re-record.
	Synthetic bool `json:"synthetic,omitempty"`
}

func TestRecipeFixtures(t *testing.T) {
	dirs, err := filepath.Glob("fixtures/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	reg, err := opportunity.LoadFromDir("../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("load kind registry: %v", err)
	}

	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			meta := readJSON[fixtureMeta](t, filepath.Join(dir, "fixture.json"))
			rec := readJSON[recipe.Recipe](t, filepath.Join(dir, "recipe.json"))

			var fetcher recipe.Fetcher
			if *update && !meta.Synthetic {
				live := recipe.NewHTTPFetcher(
					httpx.NewClient(20*time.Second, "opportunities-recipe-fixture/1.0"))
				fetcher = newRecordingFetcher(t, dir, live)
			} else {
				fetcher = newReplayFetcher(t, dir)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			samples := make([]recipe.SamplePage, 0, len(meta.SampleURLs))
			for _, u := range meta.SampleURLs {
				body, status, ferr := fetcher.Get(ctx, u)
				if ferr != nil || status != 200 {
					t.Fatalf("sample %s: status=%d err=%v", u, status, ferr)
				}
				samples = append(samples, recipe.SamplePage{URL: u, HTML: string(body)})
			}

			rep := recipecheck.Check(ctx, fetcher, reg, meta.Source, &rec, samples)
			for _, s := range rep.Detail.PerSample {
				if !s.OK {
					t.Logf("sample FAIL %s: missing=%v err=%s", s.URL, s.Missing, s.Error)
				}
			}
			if !rep.OK(passThreshold) {
				t.Fatalf("recipe does NOT crawl %s correctly: %s", meta.Source.BaseURL, rep.Summary())
			}
		})
	}
}

// ── fixture page store ──────────────────────────────────────────────

// pageKey maps a URL to its recorded filename (hash keeps it filesystem-safe).
func pageKey(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:8]) + ".body"
}

type manifest map[string]string // url -> filename

// replayFetcher serves recorded bodies; any URL the recording never saw
// fails the test loudly (a recipe drifting onto new URLs is a finding,
// not something to paper over).
type replayFetcher struct {
	t     *testing.T
	dir   string
	pages manifest
}

func newReplayFetcher(t *testing.T, dir string) *replayFetcher {
	return &replayFetcher{t: t, dir: dir, pages: readJSON[manifest](t, filepath.Join(dir, "pages", "pages.json"))}
}

func (f *replayFetcher) Get(_ context.Context, url string) ([]byte, int, error) {
	name, ok := f.pages[url]
	if !ok {
		return nil, 404, fmt.Errorf("fixture %s has no recording for %s (re-record with -update)", f.dir, url)
	}
	body, err := os.ReadFile(filepath.Join(f.dir, "pages", name))
	if err != nil {
		return nil, 0, err
	}
	return body, 200, nil
}

// recordingFetcher fetches live and snapshots every successful page.
type recordingFetcher struct {
	t     *testing.T
	dir   string
	live  recipe.Fetcher
	pages manifest
}

func newRecordingFetcher(t *testing.T, dir string, live recipe.Fetcher) *recordingFetcher {
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatalf("mkdir pages: %v", err)
	}
	f := &recordingFetcher{t: t, dir: dir, live: live, pages: manifest{}}
	t.Cleanup(func() {
		writeJSON(t, filepath.Join(dir, "pages", "pages.json"), f.pages)
	})
	return f
}

func (f *recordingFetcher) Get(ctx context.Context, url string) ([]byte, int, error) {
	body, status, err := f.live.Get(ctx, url)
	if err == nil && status == 200 {
		name := pageKey(url)
		if werr := os.WriteFile(filepath.Join(f.dir, "pages", name), body, 0o644); werr != nil {
			f.t.Fatalf("record %s: %v", url, werr)
		}
		f.pages[url] = name
	}
	return body, status, err
}

// ── helpers ─────────────────────────────────────────────────────────

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	var v T
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return v
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
