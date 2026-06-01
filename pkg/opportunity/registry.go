package opportunity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/stawi-opportunities/opportunities/pkg/definitions"
)

// Registry holds the kinds loaded at boot.
//
// All reads (Lookup/Known/Resolve) take an RLock so a Replace performed
// by the definitions-change subscriber never tears a concurrent reader.
type Registry struct {
	mu    sync.RWMutex
	specs map[string]Spec
}

// NewRegistry returns an empty Registry. Used by LoadFromDefinitions to
// accumulate parsed specs; LoadFromDir builds a Registry directly via
// the struct literal because it already owns the full map at construction.
func NewRegistry() *Registry {
	return &Registry{specs: map[string]Spec{}}
}

// register adds spec to the registry, rejecting duplicate Kind or
// URLPrefix. Caller must hold the write lock — used by LoadFromDefinitions
// during its single-threaded build phase.
func (r *Registry) register(s Spec) error {
	if _, exists := r.specs[s.Kind]; exists {
		return fmt.Errorf("duplicate kind %q", s.Kind)
	}
	for k, existing := range r.specs {
		if existing.URLPrefix == s.URLPrefix {
			return fmt.Errorf("duplicate url_prefix %q (kinds %q and %q)", s.URLPrefix, k, s.Kind)
		}
	}
	r.specs[s.Kind] = s
	return nil
}

// Replace atomically swaps the registry's contents with another's.
// Used by definitions.changed.v1 subscribers to live-update without
// taking the app down. Readers in flight see either old or new state —
// no half-built registry is ever observed.
func (r *Registry) Replace(other *Registry) {
	if other == nil {
		return
	}
	other.mu.RLock()
	clone := make(map[string]Spec, len(other.specs))
	for k, v := range other.specs {
		clone[k] = v
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.specs = clone
	r.mu.Unlock()
}

// LoadFromDir walks dir for *.yaml files, parses each as a Spec, validates,
// and returns a Registry. Duplicate kinds or duplicate url_prefixes are
// rejected. The order of files on disk does not matter.
func LoadFromDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("opportunity: read %s: %w", dir, err)
	}
	reg := NewRegistry()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("opportunity: read %s: %w", path, err)
		}
		var s Spec
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("opportunity: parse %s: %w", path, err)
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("opportunity: %s: %w", path, err)
		}
		if err := reg.register(s); err != nil {
			return nil, fmt.Errorf("opportunity: %s: %w", path, err)
		}
	}
	return reg, nil
}

// LoadFromDefinitions builds a Registry from kind YAMLs read through a
// pkg/definitions.Loader. Used in place of LoadFromDir when the app is
// wired through the definitions service.
//
// Errors on the first invalid spec — kind definitions are critical
// infrastructure; a half-built registry is worse than no registry.
func LoadFromDefinitions(ctx context.Context, loader definitions.Loader) (*Registry, error) {
	entries, err := loader.List(ctx, definitions.TypeKind)
	if err != nil {
		return nil, fmt.Errorf("opportunity: list kinds: %w", err)
	}
	reg := NewRegistry()
	for _, e := range entries {
		body, _, err := loader.Get(ctx, definitions.TypeKind, e.Name)
		if err != nil {
			return nil, fmt.Errorf("opportunity: get %s: %w", e.Name, err)
		}
		var s Spec
		if err := yaml.Unmarshal(body, &s); err != nil {
			return nil, fmt.Errorf("opportunity: parse %s: %w", e.Name, err)
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("opportunity: validate %s: %w", e.Name, err)
		}
		if err := reg.register(s); err != nil {
			return nil, fmt.Errorf("opportunity: register %s: %w", e.Name, err)
		}
	}
	return reg, nil
}

// Lookup returns the Spec for kind and a boolean indicating presence.
func (r *Registry) Lookup(kind string) (Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.specs[kind]
	return s, ok
}

// Known returns the registered kinds in alphabetical order.
func (r *Registry) Known() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.specs))
	for k := range r.specs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Resolve returns the Spec for kind, falling back to a permissive default
// when kind is unknown. Use this on the publish/index path so an in-flight
// record doesn't crash the pipeline if its kind YAML was disabled.
func (r *Registry) Resolve(kind string) Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.specs[kind]; ok {
		return s
	}
	return Spec{Kind: kind, DisplayName: kind, URLPrefix: kind, IssuingEntityLabel: "Issuer"}
}
