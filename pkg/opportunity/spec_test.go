package opportunity

import "testing"

func TestSpec_RequireKind(t *testing.T) {
	s := Spec{}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for missing Kind")
	}
}

func TestSpec_RequireURLPrefix(t *testing.T) {
	s := Spec{Kind: "job", DisplayName: "Job"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for missing URLPrefix")
	}
}

func TestSpec_URLPrefixFormat(t *testing.T) {
	for _, bad := range []string{"Jobs", "j obs", "jobs/", "JOBS"} {
		s := Spec{Kind: "job", DisplayName: "Job", URLPrefix: bad, IssuingEntityLabel: "Company"}
		if err := s.Validate(); err == nil {
			t.Errorf("expected error for url_prefix=%q", bad)
		}
	}
}

func TestSpec_ValidatePass(t *testing.T) {
	s := Spec{
		Kind:               "job",
		DisplayName:        "Job",
		IssuingEntityLabel: "Company",
		URLPrefix:          "jobs",
		UniversalRequired:  []string{"title", "description"},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpec_RequireDisplayName(t *testing.T) {
	s := Spec{Kind: "job", IssuingEntityLabel: "Company", URLPrefix: "jobs"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for missing DisplayName")
	}
}

func TestSpec_RequireIssuingEntityLabel(t *testing.T) {
	s := Spec{Kind: "job", DisplayName: "Job", URLPrefix: "jobs"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for missing IssuingEntityLabel")
	}
}

func TestSpec_UniversalRequiredRejectsUnknownKey(t *testing.T) {
	s := Spec{
		Kind: "job", DisplayName: "Job", IssuingEntityLabel: "Company",
		URLPrefix:         "jobs",
		UniversalRequired: []string{"title", "salary_min"}, // salary_min is not an envelope key
	}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for unknown envelope key")
	}
}
