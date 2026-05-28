// Package spec implements declarative connectors driven by YAML specs
// loaded from definitions/connector/*.yaml. Six concrete types live in
// sibling packages; this file holds the shared interface, the common
// ConnectorSpec shape, and the dispatch layer that binds a parsed spec
// to its per-type implementation.
package spec

import (
	"context"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SpecType identifies which concrete impl handles a given spec.
type SpecType string

// Known spec types. Each value corresponds to a sibling package that
// registers an Impl in its init() via Register.
const (
	TypeHTMLListing     SpecType = "htmllisting"
	TypeJSONFeed        SpecType = "jsonfeed"
	TypeRSSFeed         SpecType = "rssfeed"
	TypeSitemap         SpecType = "sitemap"
	TypeSchemaOrgJSONLD SpecType = "schemaorgjsonld"
	TypeXMLFeed         SpecType = "xmlfeed"
)

// ConnectorSpec is the YAML shape loaded from definitions/connector/.
type ConnectorSpec struct {
	Type       SpecType                  `yaml:"type"`
	Name       string                    `yaml:"name,omitempty"` // optional human-readable; defaults to file stem
	ListURL    string                    `yaml:"list_url"`
	Pagination *Pagination               `yaml:"pagination,omitempty"`
	Items      string                    `yaml:"item_selector,omitempty"` // CSS/JSONPath/XPath; semantics per type
	Fields     map[string]FieldExtractor `yaml:"fields,omitempty"`
	Headers    map[string]string         `yaml:"headers,omitempty"`
	DelayMS    int                       `yaml:"delay_ms,omitempty"`
	TimeoutMS  int                       `yaml:"timeout_ms,omitempty"`

	// Sitemap carries options that only apply to the sitemap impl.
	// Other types must leave it unset. Inlined into the top-level YAML
	// via an explicit `sitemap:` key so a single ConnectorSpec stays
	// readable without exploding into per-type sub-shapes.
	Sitemap *SitemapOptions `yaml:"sitemap,omitempty"`
}

// SitemapOptions carries sitemap-specific knobs. The sitemap impl is
// the sole consumer; ParseSpec does not enforce these fields.
type SitemapOptions struct {
	// IncludePatterns gates URLs by substring match. A URL must contain
	// at least one pattern to be emitted; prefix a pattern with "re:"
	// to treat it as a regular expression.
	IncludePatterns []string `yaml:"include_patterns,omitempty"`

	// ExcludePatterns drops URLs that contain any matching substring
	// (regex with "re:" prefix is supported, same as include).
	ExcludePatterns []string `yaml:"exclude_patterns,omitempty"`

	// FollowIndex enables recursion into <sitemapindex> entries.
	FollowIndex bool `yaml:"follow_index,omitempty"`

	// DetailFetch, when true, fetches each candidate URL and runs the
	// detail_fallback_type impl over the body to extract structured
	// data (JobPosting JSON-LD by default). When false, candidate URLs
	// are emitted as URL-only stubs so the LLM-extraction pipeline can
	// take over later.
	DetailFetch bool `yaml:"detail_fetch,omitempty"`

	// DetailFallbackType names the impl used for detail-fetch parsing.
	// Defaults to "schemaorgjsonld" when unset.
	DetailFallbackType string `yaml:"detail_fallback_type,omitempty"`
}

// Pagination drives how the iterator walks pages.
type Pagination struct {
	Kind          string `yaml:"kind"` // page | offset | cursor | next_link | none
	Start         int    `yaml:"start,omitempty"`
	Step          int    `yaml:"step,omitempty"`
	Max           int    `yaml:"max,omitempty"`
	CursorPath    string `yaml:"cursor_path,omitempty"` // for JSON cursor pagination
	InitialCursor string `yaml:"initial_cursor,omitempty"`
	StopOnEmpty   bool   `yaml:"stop_on_empty,omitempty"`
}

// FieldExtractor accepts either a string shorthand ("selector::text")
// or a struct with selector + parse_as.
type FieldExtractor struct {
	Selector string `yaml:"selector"`
	ParseAs  string `yaml:"parse_as,omitempty"` // iso | relative_time | epoch | unix_ms | html_to_markdown
}

// UnmarshalYAML accepts either a scalar shorthand ("selector::text") or
// a mapping with explicit selector + parse_as keys.
func (f *FieldExtractor) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		f.Selector = value.Value
		return nil
	}
	var aux struct {
		Selector string `yaml:"selector"`
		ParseAs  string `yaml:"parse_as,omitempty"`
	}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	f.Selector = aux.Selector
	f.ParseAs = aux.ParseAs
	return nil
}

// Validate checks the shape of a parsed spec. Returns the first problem
// found; never partial.
func (s *ConnectorSpec) Validate() error {
	if s.Type == "" {
		return fmt.Errorf("spec.type required")
	}
	switch s.Type {
	case TypeHTMLListing, TypeJSONFeed, TypeRSSFeed, TypeSitemap, TypeSchemaOrgJSONLD, TypeXMLFeed:
	default:
		return fmt.Errorf("spec.type %q unknown", s.Type)
	}
	if s.ListURL == "" {
		return fmt.Errorf("spec.list_url required")
	}
	if len(s.Fields) == 0 && s.Type != TypeSitemap {
		return fmt.Errorf("spec.fields required for type %s", s.Type)
	}
	if s.Pagination != nil {
		switch s.Pagination.Kind {
		case "page", "offset", "cursor", "next_link", "none", "":
		default:
			return fmt.Errorf("spec.pagination.kind %q unknown", s.Pagination.Kind)
		}
	}
	if s.DelayMS < 0 || s.DelayMS > 60_000 {
		return fmt.Errorf("spec.delay_ms must be 0..60000")
	}
	if s.TimeoutMS < 0 || s.TimeoutMS > 120_000 {
		return fmt.Errorf("spec.timeout_ms must be 0..120000")
	}
	return nil
}

// ParseSpec parses YAML into a ConnectorSpec and validates it.
func ParseSpec(body []byte) (*ConnectorSpec, error) {
	var s ConnectorSpec
	if err := yaml.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("spec yaml: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Impl is the per-type contract that each concrete spec-driven
// connector implements. The six implementations live in sibling
// packages and register themselves via Register() from init().
type Impl interface {
	Crawl(ctx context.Context, src domain.Source, client *httpx.Client, spec *ConnectorSpec) connectors.CrawlIterator
}

var (
	registryMu sync.RWMutex
	registry   = map[SpecType]Impl{}
)

// Register binds an Impl to a SpecType. Concrete impl packages call
// this from their init() function. Re-registration overwrites the
// previous binding (useful in tests).
func Register(t SpecType, impl Impl) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[t] = impl
}

// lookup returns the registered Impl for a given SpecType, if any.
func lookup(t SpecType) (Impl, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	impl, ok := registry[t]
	return impl, ok
}

// Connector implements connectors.Connector by dispatching to the
// per-type impl. Constructed once per spec file by NewFromYAML.
type Connector struct {
	spec   *ConnectorSpec
	name   string
	client *httpx.Client
	impl   Impl
}

// NewFromYAML constructs a Connector from a spec body. The connector's
// Type() reflects the spec's `name` field (or the file stem set by the
// caller). Returns an error if the spec is malformed OR no impl is
// registered for the spec's type.
func NewFromYAML(name string, body []byte, client *httpx.Client) (*Connector, error) {
	s, err := ParseSpec(body)
	if err != nil {
		return nil, fmt.Errorf("connector %s: %w", name, err)
	}
	impl, ok := lookup(s.Type)
	if !ok {
		return nil, fmt.Errorf("connector %s: no impl registered for type %q", name, s.Type)
	}
	if s.Name != "" {
		name = s.Name
	}
	return &Connector{spec: s, name: name, client: client, impl: impl}, nil
}

// Type returns the SourceType this connector handles. Mirrors the
// spec's name (or the file stem the caller supplied).
func (c *Connector) Type() domain.SourceType { return domain.SourceType(c.name) }

// Crawl delegates to the bound per-type implementation.
func (c *Connector) Crawl(ctx context.Context, src domain.Source) connectors.CrawlIterator {
	return c.impl.Crawl(ctx, src, c.client, c.spec)
}
