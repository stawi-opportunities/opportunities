package authmanifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// Store holds the manifests loaded at boot, indexed by source_type.
// The map is built once and read-only for the rest of the process.
type Store struct {
	manifests map[domain.SourceType]*Manifest
}

// LoadFromDir walks dir for *.yaml files, parses and validates each as
// a Manifest, and returns a Store. Duplicate source_type entries are
// rejected so a misconfigured definitions/ directory fails at boot
// rather than silently shadowing one manifest with another. Returns an
// empty Store when dir does not exist — the caller decides whether
// that is a fatal misconfiguration.
func LoadFromDir(dir string) (*Store, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{manifests: map[domain.SourceType]*Manifest{}}, nil
		}
		return nil, fmt.Errorf("authmanifest: read %s: %w", dir, err)
	}
	out := map[domain.SourceType]*Manifest{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("authmanifest: read %s: %w", path, err)
		}
		var m Manifest
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("authmanifest: parse %s: %w", path, err)
		}
		if err := m.Validate(); err != nil {
			return nil, fmt.Errorf("authmanifest: %s: %w", path, err)
		}
		if _, exists := out[m.SourceType]; exists {
			return nil, fmt.Errorf("authmanifest: duplicate source_type %q (in %s)", m.SourceType, path)
		}
		mm := m
		out[m.SourceType] = &mm
	}
	return &Store{manifests: out}, nil
}

// Lookup returns the Manifest for sourceType plus a boolean indicating
// presence. Lookup never returns a nil Manifest — callers can safely
// dereference when ok is true.
func (s *Store) Lookup(sourceType domain.SourceType) (*Manifest, bool) {
	m, ok := s.manifests[sourceType]
	return m, ok
}

// All returns every manifest, sorted by source_type for deterministic
// API output and stable golden tests.
func (s *Store) All() []*Manifest {
	out := make([]*Manifest, 0, len(s.manifests))
	for _, m := range s.manifests {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceType < out[j].SourceType })
	return out
}

// Known returns the registered source_type values in alphabetical order.
func (s *Store) Known() []domain.SourceType {
	out := make([]domain.SourceType, 0, len(s.manifests))
	for k := range s.manifests {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ExtensionView returns every manifest's ExtensionView, sorted. This is
// what /sources/auth-manifest serializes. Returning a slice (not a map)
// gives the extension a stable iteration order.
func (s *Store) ExtensionView() []ExtensionView {
	all := s.All()
	out := make([]ExtensionView, 0, len(all))
	for _, m := range all {
		out = append(out, m.ExtensionView())
	}
	return out
}
