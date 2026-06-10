// Package recipe defines AI-generated, deterministic extraction recipes and
// the engine that runs them. This file holds the data types and structural
// validation. Nothing here calls an LLM or a database.
package recipe

import (
	"errors"
	"fmt"
	"time"

	"github.com/andybalholm/cascadia"
)

// validFromSources are the data planes a FieldExtractor may read from, in the
// order callers typically list them (structured data first, selectors last).
var validFromSources = map[string]bool{
	"json_ld": true, "next_data": true, "microdata": true,
	"selector": true, "meta": true, "record": true, "const": true,
}

// FieldExtractor describes how to pull ONE value from a page or record. From is
// tried in order; the first source that yields a non-empty value wins. The
// resolved value is then piped through Transform.
type FieldExtractor struct {
	From      []string `json:"from,omitempty"`
	JSONPath  string   `json:"json_path,omitempty"`
	Microdata string   `json:"microdata,omitempty"`
	Selector  string   `json:"selector,omitempty"`
	Attr      string   `json:"attr,omitempty"`
	Meta      string   `json:"meta,omitempty"`
	Const     string   `json:"const,omitempty"`
	Transform []string `json:"transform,omitempty"`
	Required  bool     `json:"required,omitempty"`
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
	Pagination   Pagination        `json:"pagination"`
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

// requiredEnvelopeFields are the universal fields opportunity.Verify() demands;
// every recipe must specify how to extract each.
func (d DetailRule) requiredEnvelopeFields() map[string]FieldExtractor {
	return map[string]FieldExtractor{
		"title":          d.Title,
		"description":    d.Description,
		"issuing_entity": d.IssuingEntity,
		"apply_url":      d.ApplyURL,
		"anchor_country": d.AnchorCountry,
	}
}

// Normalize absorbs harmless LLM-output noise before Validate: an omitted
// pagination mode means "none", and transforms that don't exist in the registry
// are dropped (small models hallucinate names like "contains"; extraction
// without them still runs, and the pass-rate gate remains the real quality
// arbiter). This keeps the bounded repair loop for genuine structural errors
// instead of burning attempts on cosmetic ones.
func (r *Recipe) Normalize() {
	if r.List.Pagination.Mode == "" {
		r.List.Pagination.Mode = "none"
	}
	clean := func(fx *FieldExtractor) {
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
	}

	check("list.link", r.List.Link)
	check("list.pagination.cursor", r.List.Pagination.Cursor)
	check("list.pagination.next", r.List.Pagination.Next)
	check("kind.path", r.Kind.Path)
	for name, fx := range r.Detail.requiredEnvelopeFields() {
		check("detail."+name, fx)
	}
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
