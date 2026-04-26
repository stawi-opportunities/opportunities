package opportunity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry holds the kinds loaded at boot.
type Registry struct {
	specs map[string]Spec
}

// LoadFromDir walks dir for *.yaml files, parses each as a Spec, validates,
// and returns a Registry. Duplicate kinds or duplicate url_prefixes are
// rejected. The order of files on disk does not matter.
func LoadFromDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("opportunity: read %s: %w", dir, err)
	}
	specs := map[string]Spec{}
	prefixes := map[string]string{}
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
		if _, exists := specs[s.Kind]; exists {
			return nil, fmt.Errorf("opportunity: duplicate kind %q (in %s)", s.Kind, path)
		}
		if other, exists := prefixes[s.URLPrefix]; exists {
			return nil, fmt.Errorf("opportunity: duplicate url_prefix %q (kinds %q and %q)", s.URLPrefix, other, s.Kind)
		}
		specs[s.Kind] = s
		prefixes[s.URLPrefix] = s.Kind
	}
	return &Registry{specs: specs}, nil
}

// Lookup returns the Spec for kind and a boolean indicating presence.
func (r *Registry) Lookup(kind string) (Spec, bool) {
	s, ok := r.specs[kind]
	return s, ok
}

// Known returns the registered kinds in alphabetical order.
func (r *Registry) Known() []string {
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
	if s, ok := r.specs[kind]; ok {
		return s
	}
	return Spec{Kind: kind, DisplayName: kind, URLPrefix: kind, IssuingEntityLabel: "Issuer"}
}
