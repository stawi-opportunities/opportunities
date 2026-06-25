package authmanifest_test

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/authmanifest"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func validExtensionManifest() authmanifest.Manifest {
	return authmanifest.Manifest{
		SourceType:    domain.SourceBrighterMonday,
		AuthMethod:    authmanifest.AuthExtension,
		DisplayName:   "BrighterMonday",
		LoginURL:      "https://www.brightermonday.co.ke/login",
		CookieDomains: []string{".brightermonday.co.ke"},
		ApplyFlow: authmanifest.ApplyFlow{
			Type:           authmanifest.ApplyHTTPForm,
			FormURLPattern: `^https://www\.brightermonday\.co\.ke/job/.+/apply$`,
			Fields: map[string]authmanifest.FieldMap{
				"cv": {Name: "resume", Source: "cv_bytes"},
			},
		},
	}
}

func TestManifest_Validate_Happy(t *testing.T) {
	m := validExtensionManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestManifest_Validate_Errors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*authmanifest.Manifest)
	}{
		{"missing source_type", func(m *authmanifest.Manifest) { m.SourceType = "" }},
		{"bad auth_method", func(m *authmanifest.Manifest) { m.AuthMethod = "bogus" }},
		{"extension without login_url", func(m *authmanifest.Manifest) { m.LoginURL = "" }},
		{"extension without cookie_domains", func(m *authmanifest.Manifest) { m.CookieDomains = nil }},
		{"bad apply_flow.type", func(m *authmanifest.Manifest) {
			m.ApplyFlow.Type = "neither"
		}},
		{"bad form_url_pattern regex", func(m *authmanifest.Manifest) {
			m.ApplyFlow.FormURLPattern = "[unterminated"
		}},
		{"csrf cookie without cookie name", func(m *authmanifest.Manifest) {
			m.ApplyFlow.CSRF = &authmanifest.CSRF{Source: "cookie"}
		}},
		{"csrf form_token without field", func(m *authmanifest.Manifest) {
			m.ApplyFlow.CSRF = &authmanifest.CSRF{Source: "form_token"}
		}},
		{"csrf empty source", func(m *authmanifest.Manifest) {
			m.ApplyFlow.CSRF = &authmanifest.CSRF{Source: ""}
		}},
		{"csrf unknown source", func(m *authmanifest.Manifest) {
			m.ApplyFlow.CSRF = &authmanifest.CSRF{Source: "header"}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := validExtensionManifest()
			tc.mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestManifest_MatchesApplyURL(t *testing.T) {
	m := validExtensionManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.brightermonday.co.ke/job/12345/apply", true},
		{"https://www.brightermonday.co.ke/job/abc-def/apply", true},
		{"https://www.brightermonday.co.ke/job/", false},
		{"https://www.brightermonday.co.ke/login", false},
		{"https://www.linkedin.com/jobs/123/apply", false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			if got := m.MatchesApplyURL(tc.url); got != tc.want {
				t.Fatalf("MatchesApplyURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestManifest_MatchesApplyURL_NoPattern(t *testing.T) {
	m := validExtensionManifest()
	m.ApplyFlow.FormURLPattern = ""
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if m.MatchesApplyURL("https://anything") {
		t.Fatal("manifest with no pattern should not match any URL")
	}
}

func TestManifest_ExtensionView_StripsServerOnlyFields(t *testing.T) {
	m := validExtensionManifest()
	m.InstructionsMD = "secret instructions"
	m.ApplyFlow.Fields["sensitive"] = authmanifest.FieldMap{Name: "n", Source: "s"}
	v := m.ExtensionView()
	if v.SourceType != m.SourceType {
		t.Fatalf("source_type mismatch")
	}
	if v.LoginURL == "" {
		t.Fatal("login_url should be present")
	}
	// ExtensionView has no field for instructions or apply_flow.fields —
	// the compile-time absence is the test. Nothing more to assert.
}

func TestManifest_UIView_IncludesInstructions(t *testing.T) {
	m := validExtensionManifest()
	m.InstructionsMD = "# Connect BrighterMonday\n\n1. ..."
	v := m.UIView()
	if v.InstructionsMD != m.InstructionsMD {
		t.Fatalf("UIView should expose instructions_md")
	}
	if v.DisplayName != m.DisplayName {
		t.Fatalf("UIView should expose display_name")
	}
}
