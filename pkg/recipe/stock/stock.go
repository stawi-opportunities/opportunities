// Package stock loads bundled extraction recipes that replace per-site Go
// connectors. Onboarding is data: attach a stock recipe (or a custom one)
// to a source of engine type "api" / "html" / etc.
package stock

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stawi-opportunities/opportunities/pkg/recipe"
)

// DefaultDir is the repo-relative path used when STOCK_RECIPES_DIR is unset
// and the process is run from the module root (dev). Production should set
// STOCK_RECIPES_DIR to the mounted ConfigMap / definitions path.
const DefaultDir = "definitions/stock-recipes"

var (
	mu    sync.RWMutex
	cache map[string]*recipe.Recipe
	byHost map[string]string // host → stock name
)

// LoadDir reads every *.json recipe from dir into the process-wide cache.
// Safe to call multiple times; last load wins.
func LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("stock recipes: read %s: %w", dir, err)
	}
	next := map[string]*recipe.Recipe{}
	hosts := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			return fmt.Errorf("stock recipes: read %s: %w", e.Name(), rerr)
		}
		var rec recipe.Recipe
		if uerr := json.Unmarshal(raw, &rec); uerr != nil {
			return fmt.Errorf("stock recipes: parse %s: %w", e.Name(), uerr)
		}
		rec.Normalize()
		if verr := rec.Validate(); verr != nil {
			return fmt.Errorf("stock recipes: validate %s: %w", e.Name(), verr)
		}
		next[name] = &rec
		// Index host from list.endpoint when absolute.
		if host := hostOf(rec.List.Endpoint); host != "" {
			hosts[host] = name
		}
	}
	mu.Lock()
	cache = next
	byHost = hosts
	mu.Unlock()
	return nil
}

// LoadDefault tries STOCK_RECIPES_DIR, then DefaultDir, then
// /etc/stock-recipes (container convention). Returns nil if a dir loads;
// returns the last error if none exist (callers may treat as optional).
func LoadDefault() error {
	candidates := []string{}
	if d := strings.TrimSpace(os.Getenv("STOCK_RECIPES_DIR")); d != "" {
		candidates = append(candidates, d)
	}
	candidates = append(candidates, DefaultDir, "/etc/stock-recipes")
	var last error
	for _, d := range candidates {
		if err := LoadDir(d); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return last
}

// Get returns a copy-ready stock recipe by short name (e.g. "remoteok"),
// or nil if unknown.
func Get(name string) *recipe.Recipe {
	mu.RLock()
	defer mu.RUnlock()
	rec := cache[strings.ToLower(strings.TrimSpace(name))]
	if rec == nil {
		return nil
	}
	// Shallow copy so callers can mutate Version etc. without racing.
	cp := *rec
	return &cp
}

// Names returns sorted stock recipe names currently loaded.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(cache))
	for n := range cache {
		out = append(out, n)
	}
	return out
}

// LookupByBaseURL returns a stock recipe when the source base_url host
// matches a known stock endpoint host (remoteok.com, himalayas.app, …).
func LookupByBaseURL(baseURL string) (name string, rec *recipe.Recipe) {
	host := hostOf(baseURL)
	if host == "" {
		return "", nil
	}
	// Strip www.
	host = strings.TrimPrefix(host, "www.")
	mu.RLock()
	defer mu.RUnlock()
	// Direct or suffix match (api.example.com → example.com stock).
	if n, ok := byHost[host]; ok {
		return n, clone(cache[n])
	}
	for h, n := range byHost {
		hh := strings.TrimPrefix(h, "www.")
		if host == hh || strings.HasSuffix(host, "."+hh) {
			return n, clone(cache[n])
		}
	}
	// Also try stock name == first label (legacy seed types).
	if rec := cache[host]; rec != nil {
		return host, clone(rec)
	}
	return "", nil
}

// LookupLegacyType maps old per-board SourceType values to stock recipes
// so existing DB rows keep working after the engine-only registry change.
func LookupLegacyType(sourceType string) (name string, rec *recipe.Recipe) {
	key := strings.ToLower(strings.TrimSpace(sourceType))
	// Historical type names == stock file names.
	if rec := Get(key); rec != nil {
		return key, rec
	}
	return "", nil
}

func clone(r *recipe.Recipe) *recipe.Recipe {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
