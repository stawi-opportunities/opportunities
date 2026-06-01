// Package definitions exposes a typed loader for the long-tail
// customization surface — opportunity-kind YAMLs, extraction
// prompts, source seeds, declarative connector specs. The loader
// fronts R2 (production), with in-memory test impl + a 5-min
// refresh + NATS broadcast for instant-on-edit propagation.
//
// Lifecycle:
//
//  1. Boot — Loader.Start(ctx) populates the in-memory cache from
//     R2 (List + Get every active object).
//  2. Tick — every refresh interval, re-list R2; for any object
//     whose ETag changed since last fetch, re-fetch + fire
//     subscribers.
//  3. Push — on opportunities.definitions.changed.v1 with type+name,
//     re-fetch that one object immediately and fire subscribers.
package definitions

import (
	"context"
	"errors"
	"time"
)

// Type discriminates what kind of definition we're loading. Each maps
// to a sub-prefix under the R2 bucket (definitions/<type>/<name>.yaml).
type Type string

const (
	// TypeKind is an opportunity-kind YAML (job, scholarship, tender, etc.)
	// parsed via opportunity.Spec.
	TypeKind Type = "kind"
	// TypePrompt is a free-form extraction prompt fragment. Currently
	// unused by the loader interface — kind prompts live in the kind
	// YAML — but reserved for per-kind prompt overrides shipped without
	// changing the spec body.
	TypePrompt Type = "prompt"
	// TypeConnector is a declarative connector spec (htmllisting,
	// jsonfeed, etc.) used by Plan B2's spec-driven connectors.
	TypeConnector Type = "connector"
	// TypeSeed is a source-seed YAML — bulk import of new sources
	// without round-tripping each through the discovery flow.
	TypeSeed Type = "seed"
)

// Entry is one item in a List() result.
type Entry struct {
	Type      Type      `json:"type"`
	Name      string    `json:"name"`
	Version   string    `json:"version"` // R2 ETag or object version
	UpdatedAt time.Time `json:"updated_at"`
	Size      int64     `json:"size"`
}

// Loader is the read-only public face. Tests construct MemoryLoader;
// production constructs R2Loader via NewR2Loader.
type Loader interface {
	// Start begins the background refresh. Must be called once
	// before Get / List. Blocks until the initial population
	// succeeds; returns the start error if the first List fails
	// (no point booting if R2 is broken).
	Start(ctx context.Context) error

	// Stop ends the background refresh. Safe to call multiple times.
	Stop()

	// Get returns the active body + version for (type, name).
	// Returns ErrNotFound when the definition was deleted or never
	// existed. Stale-cache reads are OK — the loader prioritises
	// availability over freshness on R2 outage.
	Get(ctx context.Context, t Type, name string) (body []byte, version string, err error)

	// List returns every active definition of a type. Sorted by Name.
	List(ctx context.Context, t Type) ([]Entry, error)

	// Subscribe registers a callback fired whenever a definition
	// of `t` changes (refresh tick OR NATS push). The callback
	// runs on the loader's goroutine — keep it non-blocking.
	// Returns an unsubscribe func.
	Subscribe(t Type, fn func(name string, version string)) func()
}

// ErrNotFound is returned by Get when the definition has been
// deleted or never existed.
var ErrNotFound = errors.New("definitions: not found")
