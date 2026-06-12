// Package recipe defines AI-generated, deterministic extraction recipes and
// the engine that runs them. This file holds the data types and structural
// validation. Nothing here calls an LLM or a database.
package recipe

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"github.com/antchfx/xpath"
)

// validFromSources are the data planes a FieldExtractor may read from, in the
// order callers typically list them (structured data first, selectors last).
var validFromSources = map[string]bool{
	"json_ld": true, "next_data": true, "microdata": true,
	"selector": true, "xpath": true, "meta": true, "record": true, "const": true, "page_url": true, "tenant": true,
}

// FieldExtractor describes how to pull ONE value from a page or record. From is
// tried in order; the first source that yields a non-empty value wins. The
// resolved value is then piped through Transform.
type FieldExtractor struct {
	From      []string      `json:"from,omitempty"`
	JSONPath  string        `json:"json_path,omitempty"`
	Microdata string        `json:"microdata,omitempty"`
	Selector  string        `json:"selector,omitempty"`
	XPath     string        `json:"xpath,omitempty"`
	Attr      string        `json:"attr,omitempty"`
	Meta      string        `json:"meta,omitempty"`
	Const     string        `json:"const,omitempty"`
	Transform TransformList `json:"transform,omitempty"`
	Required  bool          `json:"required,omitempty"`
}

// UnmarshalJSON accepts the canonical object form AND the bare-string shorthand
// LLMs sometimes emit where an extractor belongs (e.g. "pagination": {"next":
// "a.next"}). A bare string is read as a CSS-selector extractor — the most
// plausible meaning — and the pass-rate gate remains the quality arbiter.
// Any other non-object shape yields an empty extractor rather than a parse
// failure that burns the repair loop.
func (fx *FieldExtractor) UnmarshalJSON(b []byte) error {
	type plain FieldExtractor // no methods: avoids recursing into this func
	var p plain
	if err := json.Unmarshal(b, &p); err == nil {
		*fx = FieldExtractor(p)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil && s != "" {
		*fx = FieldExtractor{From: []string{"selector"}, Selector: s}
		return nil
	}
	*fx = FieldExtractor{}
	return nil
}

// TransformList is a []string that unmarshals tolerantly: LLMs sometimes emit a
// bare string instead of an array, or stuff objects/numbers into the list.
// Accept strings (bare or in an array), drop everything else — Normalize() and
// the pass-rate gate handle semantic correctness.
type TransformList []string

func (t *TransformList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*t = TransformList{one}
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		*t = nil // not a string or array (object/number/null) — drop, don't fail the recipe
		return nil
	}
	out := make(TransformList, 0, len(raw))
	for _, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out = append(out, s)
		}
	}
	*t = out
	return nil
}

// empty reports whether the extractor specifies no way to produce a value.
func (fx FieldExtractor) empty() bool {
	return len(fx.From) == 0 && fx.Const == ""
}

type KindRule struct {
	Mode  string         `json:"mode"`
	Fixed string         `json:"fixed,omitempty"`
	Path  FieldExtractor `json:"path,omitzero"`
}

type Pagination struct {
	Mode     string         `json:"mode"`
	Param    string         `json:"param,omitempty"`
	Cursor   FieldExtractor `json:"cursor,omitzero"`
	Next     FieldExtractor `json:"next,omitzero"`
	MaxPages int            `json:"max_pages,omitempty"`
}

type ListRule struct {
	Mode         string            `json:"mode"`
	Endpoint     string            `json:"endpoint,omitempty"`
	Method       string            `json:"method,omitempty"`
	Params       map[string]string `json:"params,omitempty"`
	ItemsPath    string            `json:"items_path,omitempty"`
	ItemSelector string            `json:"item_selector,omitempty"`
	Link         FieldExtractor    `json:"link,omitzero"`
	// LinkPattern is the REUSABLE link-discovery primitive: a substring
	// every job-detail URL contains (e.g. "/listings/", "/job/", "/jobs/").
	// When set, the engine harvests every same-host anchor whose resolved
	// href contains it — no per-site CSS card selectors. The URL path that
	// defines "a job" is a far more stable contract than presentation
	// classes (data-cy, tailwind), so this is preferred over
	// ItemSelector+Link for HTML boards.
	LinkPattern string `json:"link_pattern,omitempty"`
	// Tenants collapses a multi-tenant ATS platform (Greenhouse, Lever)
	// into ONE source: list every board token here and put "{tenant}" in
	// the api endpoint. The engine crawls each tenant in one pass
	// (skipping dead boards), so 36 Greenhouse boards become a single
	// source row whose tenant list is data, not code. api mode only.
	Tenants    []string   `json:"tenants,omitempty"`
	Pagination Pagination `json:"pagination"`
}

type DetailRule struct {
	// RecordSource enum (api|json_ld|next_data|microdata|html) is validated by
	// the executor (Plan 2), not by Validate().
	RecordSource string `json:"record_source"`

	Title         FieldExtractor `json:"title"`
	Description   FieldExtractor `json:"description"`
	IssuingEntity FieldExtractor `json:"issuing_entity"`
	ApplyURL      FieldExtractor `json:"apply_url"`
	LocationText  FieldExtractor `json:"location_text,omitzero"`
	AnchorCountry FieldExtractor `json:"anchor_country"`
	Remote        FieldExtractor `json:"remote,omitzero"`
	PostedAt      FieldExtractor `json:"posted_at,omitzero"`
	Deadline      FieldExtractor `json:"deadline,omitzero"`
	AmountMin     FieldExtractor `json:"amount_min,omitzero"`
	AmountMax     FieldExtractor `json:"amount_max,omitzero"`
	Currency      FieldExtractor `json:"currency,omitzero"`
	Categories    FieldExtractor `json:"categories,omitzero"`

	CompanyLogoURL FieldExtractor `json:"company_logo_url,omitzero"`
	CompanyProfile FieldExtractor `json:"company_profile,omitzero"`

	Attributes map[string]FieldExtractor `json:"attributes,omitempty"`
}

type Recipe struct {
	Version      int       `json:"version"`
	GeneratedAt  time.Time `json:"generated_at"`
	Model        string    `json:"model,omitempty"`
	SampleURLs   []string  `json:"sample_urls,omitempty"`
	SampleHashes []string  `json:"sample_hashes,omitempty"`
	PassRate     float64   `json:"pass_rate,omitempty"`

	Acquisition string     `json:"acquisition"`
	Kind        KindRule   `json:"kind"`
	List        ListRule   `json:"list"`
	Detail      DetailRule `json:"detail"`
}

var validAcquisition = map[string]bool{"api": true, "structured_data": true, "selectors": true}
var validListMode = map[string]bool{"api": true, "sitemap": true, "structured_data": true, "selector": true}
var validPaginationMode = map[string]bool{"none": true, "page_param": true, "cursor": true, "next_link": true}
var validKindMode = map[string]bool{"source_default": true, "fixed": true, "by_path": true}

// requiredEnvelopeFields are the fields EVERY opportunity kind requires
// (job.yaml universal_required = title/description/issuing_entity/apply_url).
// anchor_country is NOT here: the job kind does not require it, and many
// structured feeds (Greenhouse, Lever) carry free-text location that the
// downstream normalizer geocodes into a country — so demanding a
// country extractor at recipe time is stricter than runtime Verify.
// anchor_country is still validated when present (the optional check list).
func (d DetailRule) requiredEnvelopeFields() map[string]FieldExtractor {
	return map[string]FieldExtractor{
		"title":          d.Title,
		"description":    d.Description,
		"issuing_entity": d.IssuingEntity,
		"apply_url":      d.ApplyURL,
	}
}

// Normalize absorbs harmless LLM-output noise before Validate: an omitted
// pagination mode means "none", transforms that don't exist in the registry
// are dropped (small models hallucinate names like "contains"; extraction
// without them still runs, and the pass-rate gate remains the real quality
// arbiter), and common From-source aliases are coerced onto the canonical
// names ("html"→selector, "og:title"→meta). This keeps the bounded repair
// loop for genuine structural errors instead of burning attempts on
// cosmetic ones.
func (r *Recipe) Normalize() {
	if r.List.Pagination.Mode == "" {
		r.List.Pagination.Mode = "none"
	}
	clean := func(fx *FieldExtractor) {
		// Coerce From aliases the LLMs reliably produce despite the prompt
		// whitelisting canonical names. Unknown sources are dropped (the
		// remaining sources still run; required-field emptiness is caught
		// by Validate / the pass-rate gate).
		if len(fx.From) > 0 {
			kept := fx.From[:0]
			for _, src := range fx.From {
				switch {
				case validFromSources[src]:
					kept = append(kept, src)
				case src == "html" || src == "css" || src == "dom":
					kept = append(kept, "selector")
				case strings.HasPrefix(src, "og:") || strings.HasPrefix(src, "twitter:"):
					if fx.Meta == "" {
						fx.Meta = src
					}
					kept = append(kept, "meta")
				case src == "json" || src == "jsonld" || src == "json-ld":
					kept = append(kept, "json_ld")
				case src == "url":
					kept = append(kept, "page_url")
				}
			}
			fx.From = kept
		}
		if len(fx.Transform) == 0 {
			return
		}
		kept := fx.Transform[:0]
		for _, tn := range fx.Transform {
			if transformExists(tn) {
				kept = append(kept, tn)
			}
		}
		fx.Transform = kept
	}
	clean(&r.List.Link)
	clean(&r.List.Pagination.Cursor)
	clean(&r.List.Pagination.Next)
	for _, fx := range []*FieldExtractor{
		&r.Detail.Title, &r.Detail.Description, &r.Detail.IssuingEntity,
		&r.Detail.ApplyURL, &r.Detail.LocationText, &r.Detail.AnchorCountry,
		&r.Detail.Remote, &r.Detail.PostedAt, &r.Detail.Deadline,
		&r.Detail.AmountMin, &r.Detail.AmountMax, &r.Detail.Currency,
		&r.Detail.Categories, &r.Detail.CompanyLogoURL, &r.Detail.CompanyProfile,
	} {
		clean(fx)
	}
	for name, fx := range r.Detail.Attributes {
		clean(&fx)
		r.Detail.Attributes[name] = fx
	}
}

// Validate checks a recipe's structural integrity: enum fields, that every
// required envelope field has an extractor, and that every FieldExtractor uses
// known From sources, known transforms, and parseable selectors. It does NOT
// run the recipe. Returns a joined error describing every problem found.
func (r *Recipe) Validate() error {
	var errs []error

	if !validAcquisition[r.Acquisition] {
		errs = append(errs, fmt.Errorf("acquisition %q is not one of api/structured_data/selectors", r.Acquisition))
	}
	if !validListMode[r.List.Mode] {
		errs = append(errs, fmt.Errorf("list.mode %q is invalid", r.List.Mode))
	}
	if !validPaginationMode[r.List.Pagination.Mode] {
		errs = append(errs, fmt.Errorf("list.pagination.mode %q is invalid", r.List.Pagination.Mode))
	}
	if !validKindMode[r.Kind.Mode] {
		errs = append(errs, fmt.Errorf("kind.mode %q is invalid", r.Kind.Mode))
	}
	if r.Kind.Mode == "fixed" && r.Kind.Fixed == "" {
		errs = append(errs, errors.New("kind.mode=fixed requires kind.fixed"))
	}

	for name, fx := range r.Detail.requiredEnvelopeFields() {
		if fx.empty() {
			errs = append(errs, fmt.Errorf("detail.%s: required envelope field has no extractor", name))
		}
	}

	check := func(label string, fx FieldExtractor) {
		if fx.empty() {
			return
		}
		for _, src := range fx.From {
			if !validFromSources[src] {
				errs = append(errs, fmt.Errorf("%s: unknown From source %q", label, src))
			}
		}
		for _, tn := range fx.Transform {
			if !transformExists(tn) {
				errs = append(errs, fmt.Errorf("%s: unknown transform %q", label, tn))
			}
		}
		if fx.Selector != "" {
			if _, err := cascadia.Compile(fx.Selector); err != nil {
				errs = append(errs, fmt.Errorf("%s: invalid selector %q: %w", label, fx.Selector, err))
			}
		}
		if fx.XPath != "" {
			if _, err := xpath.Compile(fx.XPath); err != nil {
				errs = append(errs, fmt.Errorf("%s: invalid xpath %q: %w", label, fx.XPath, err))
			}
		}
	}

	check("list.link", r.List.Link)
	check("list.pagination.cursor", r.List.Pagination.Cursor)
	check("list.pagination.next", r.List.Pagination.Next)
	check("kind.path", r.Kind.Path)
	for name, fx := range r.Detail.requiredEnvelopeFields() {
		check("detail."+name, fx)
	}
	check("detail.anchor_country", r.Detail.AnchorCountry)
	check("detail.location_text", r.Detail.LocationText)
	check("detail.remote", r.Detail.Remote)
	check("detail.posted_at", r.Detail.PostedAt)
	check("detail.deadline", r.Detail.Deadline)
	check("detail.amount_min", r.Detail.AmountMin)
	check("detail.amount_max", r.Detail.AmountMax)
	check("detail.currency", r.Detail.Currency)
	check("detail.categories", r.Detail.Categories)
	check("detail.company_logo_url", r.Detail.CompanyLogoURL)
	check("detail.company_profile", r.Detail.CompanyProfile)
	for k, fx := range r.Detail.Attributes {
		check("detail.attributes."+k, fx)
	}

	return errors.Join(errs...)
}
