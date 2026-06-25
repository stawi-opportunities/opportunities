// Package authmanifest defines the per-source authentication manifest
// loaded from definitions/source-auth/*.yaml at boot. A manifest tells
// the platform three things:
//
//  1. How the candidate is expected to authenticate to this source
//     (auth_method: none | extension | oauth | api_token).
//  2. What the extension should capture when the user logs in
//     (cookie_domains, required_cookies, storage_keys).
//  3. How the autoapply submitter should replay an application using
//     the captured session (apply_flow.type, form_url_pattern, fields,
//     csrf).
//
// Phase 2 ships the type, the loader, and a Store keyed by source_type;
// only `auth_method: extension` is wired in Phase 4–5. Other methods
// are reserved so the YAML schema is stable when we add them.
package authmanifest

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// AuthMethod enumerates the supported per-source authentication models.
type AuthMethod string

const (
	// AuthNone — public apply URL, no candidate identity required.
	AuthNone AuthMethod = "none"
	// AuthExtension — browser-extension session capture, the v1 path
	// for BrighterMonday, LinkedIn, Workday tenants etc.
	AuthExtension AuthMethod = "extension"
	// AuthOAuth — reserved for sources offering OAuth (e.g. some ATSes).
	AuthOAuth AuthMethod = "oauth"
	// AuthAPIToken — reserved for sources offering a personal API token
	// (e.g. Greenhouse Harvest).
	AuthAPIToken AuthMethod = "api_token"
)

// ApplyFlowType discriminates the replay strategy.
type ApplyFlowType string

const (
	// ApplyHTTPForm — server-rendered HTML form replayed via net/http +
	// goquery. Cheap. Default for v1.
	ApplyHTTPForm ApplyFlowType = "http_form"
	// ApplyHeadless — JS-rendered flow replayed via chromedp/rod.
	// Phase 8 only.
	ApplyHeadless ApplyFlowType = "headless"
)

// Manifest is the parsed shape of one YAML file under
// definitions/source-auth/.
type Manifest struct {
	SourceType      domain.SourceType `yaml:"source_type"`
	AuthMethod      AuthMethod        `yaml:"auth_method"`
	DisplayName     string            `yaml:"display_name"`
	LoginURL        string            `yaml:"login_url"`
	HomeURL         string            `yaml:"home_url"`
	CookieDomains   []string          `yaml:"cookie_domains"`
	StorageKeys     []string          `yaml:"storage_keys"`
	RequiredCookies []string          `yaml:"required_cookies"`
	SessionTTL      time.Duration     `yaml:"session_ttl"`
	DetectLoggedIn  *Probe            `yaml:"detect_logged_in,omitempty"`
	ApplyFlow       ApplyFlow         `yaml:"apply_flow"`
	InstructionsMD  string            `yaml:"instructions_md"`

	formURLRegex *regexp.Regexp // compiled at validate time
}

// Probe describes an optional sanity GET that the extension (or replay
// leg) can hit to verify "this session is actually logged in".
type Probe struct {
	Method       string `yaml:"method"        json:"method"`
	URL          string `yaml:"url"           json:"url"`
	ExpectStatus int    `yaml:"expect_status" json:"expect_status"`
}

// ApplyFlow describes how the replay leg should submit the application.
type ApplyFlow struct {
	Type           ApplyFlowType         `yaml:"type"`
	FormURLPattern string                `yaml:"form_url_pattern"`
	Fields         map[string]FieldMap   `yaml:"fields"`
	CSRF           *CSRF                 `yaml:"csrf,omitempty"`
}

// FieldMap binds a logical field name (cv_file, cover, phone, …) to a
// concrete form field name on the source and the SubmitRequest value
// that fills it.
type FieldMap struct {
	Name   string `yaml:"name"`
	Source string `yaml:"source"` // cv_bytes | cover_letter | phone | email | full_name | location | current_title | skills
}

// CSRF describes how to obtain the source's CSRF token. For sources
// that mint a token on a cookie + echo it as a header (Laravel,
// Symfony, …), Source="cookie". For sources that emit a hidden
// <input>, Source="form_token".
type CSRF struct {
	Source string `yaml:"source"` // cookie | form_token
	Cookie string `yaml:"cookie,omitempty"`
	Header string `yaml:"header,omitempty"`
	Field  string `yaml:"field,omitempty"`
}

// Validate enforces field presence and compiles regex fields. Called
// by the loader for every manifest it reads so misconfiguration fails
// at boot, not on the first capture.
func (m *Manifest) Validate() error {
	if m.SourceType == "" {
		return errors.New("source_type is required")
	}
	switch m.AuthMethod {
	case AuthNone, AuthExtension, AuthOAuth, AuthAPIToken:
	default:
		return fmt.Errorf("auth_method %q: must be one of none|extension|oauth|api_token", m.AuthMethod)
	}
	if m.AuthMethod == AuthExtension {
		if m.LoginURL == "" {
			return errors.New("extension manifests must declare login_url")
		}
		if len(m.CookieDomains) == 0 {
			return errors.New("extension manifests must declare at least one cookie_domain")
		}
	}
	if m.ApplyFlow.Type != "" {
		switch m.ApplyFlow.Type {
		case ApplyHTTPForm, ApplyHeadless:
		default:
			return fmt.Errorf("apply_flow.type %q: must be http_form|headless", m.ApplyFlow.Type)
		}
		if m.ApplyFlow.FormURLPattern != "" {
			re, err := regexp.Compile(m.ApplyFlow.FormURLPattern)
			if err != nil {
				return fmt.Errorf("apply_flow.form_url_pattern: %w", err)
			}
			m.formURLRegex = re
		}
		if m.ApplyFlow.CSRF != nil {
			switch m.ApplyFlow.CSRF.Source {
			case "cookie":
				if m.ApplyFlow.CSRF.Cookie == "" {
					return errors.New("apply_flow.csrf.cookie required when csrf.source=cookie")
				}
			case "form_token":
				if m.ApplyFlow.CSRF.Field == "" {
					return errors.New("apply_flow.csrf.field required when csrf.source=form_token")
				}
			case "":
				return errors.New("apply_flow.csrf.source required when csrf block is present")
			default:
				return fmt.Errorf("apply_flow.csrf.source %q: must be cookie|form_token", m.ApplyFlow.CSRF.Source)
			}
		}
	}
	return nil
}

// MatchesApplyURL reports whether url is in scope for this manifest's
// apply_flow. Returns false if no form_url_pattern was declared (i.e.
// the manifest doesn't claim any apply URL).
func (m *Manifest) MatchesApplyURL(url string) bool {
	if m.formURLRegex == nil {
		return false
	}
	return m.formURLRegex.MatchString(url)
}

// ExtensionView is the trimmed shape exposed to the extension via
// GET /sources/auth-manifest. It deliberately omits instructions_md
// (UI-only) and apply_flow.fields (server-side only) so the extension
// download stays small and free of operational details.
type ExtensionView struct {
	SourceType      domain.SourceType `json:"source_type"`
	AuthMethod      AuthMethod        `json:"auth_method"`
	DisplayName     string            `json:"display_name"`
	LoginURL        string            `json:"login_url"`
	HomeURL         string            `json:"home_url,omitempty"`
	CookieDomains   []string          `json:"cookie_domains"`
	StorageKeys     []string          `json:"storage_keys,omitempty"`
	RequiredCookies []string          `json:"required_cookies,omitempty"`
	DetectLoggedIn  *Probe            `json:"detect_logged_in,omitempty"`
}

// ExtensionView returns the public shape served to extension clients.
func (m *Manifest) ExtensionView() ExtensionView {
	return ExtensionView{
		SourceType:      m.SourceType,
		AuthMethod:      m.AuthMethod,
		DisplayName:     m.DisplayName,
		LoginURL:        m.LoginURL,
		HomeURL:         m.HomeURL,
		CookieDomains:   m.CookieDomains,
		StorageKeys:     m.StorageKeys,
		RequiredCookies: m.RequiredCookies,
		DetectLoggedIn:  m.DetectLoggedIn,
	}
}

// UIView is the candidate-facing shape served by
// GET /sources/:source_type/auth. It exposes the markdown instructions
// the UI renders inside the "Connected Accounts" card.
type UIView struct {
	SourceType     domain.SourceType `json:"source_type"`
	AuthMethod     AuthMethod        `json:"auth_method"`
	DisplayName    string            `json:"display_name"`
	LoginURL       string            `json:"login_url"`
	HomeURL        string            `json:"home_url,omitempty"`
	InstructionsMD string            `json:"instructions_md"`
}

// UIView returns the candidate-facing shape.
func (m *Manifest) UIView() UIView {
	return UIView{
		SourceType:     m.SourceType,
		AuthMethod:     m.AuthMethod,
		DisplayName:    m.DisplayName,
		LoginURL:       m.LoginURL,
		HomeURL:        m.HomeURL,
		InstructionsMD: m.InstructionsMD,
	}
}
