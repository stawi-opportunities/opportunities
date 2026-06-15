package otprendezvous

import "testing"

func TestNormCompany(t *testing.T) {
	cases := map[string]string{
		"Cloudflare":       "cloudflare",
		"Cloudflare, Inc.": "cloudflare",
		"Acme Corp":        "acme",
		"Foo Bar Ltd":      "foobar",
		"  Stripe  ":       "stripe",
		"":                 "",
	}
	for in, want := range cases {
		if got := NormCompany(in); got != want {
			t.Errorf("NormCompany(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompanyFromGreenhouseURL(t *testing.T) {
	cases := map[string]string{
		"https://boards.greenhouse.io/cloudflare/jobs/123":             "cloudflare",
		"https://job-boards.greenhouse.io/cloudflare/jobs/123":         "cloudflare",
		"https://boards.greenhouse.io/embed/job_app?for=acme&token=99": "acme",
		"https://example.com/careers/apply":                            "careers", // best-effort first path seg
		"not a url ::::":                                               "",
	}
	for in, want := range cases {
		if got := CompanyFromGreenhouseURL(in); got != want {
			t.Errorf("CompanyFromGreenhouseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompanyFromSubject(t *testing.T) {
	// The real Greenhouse OTP subject shape.
	got := CompanyFromSubject("Security code for your application to Cloudflare")
	if got != "cloudflare" {
		t.Errorf("CompanyFromSubject = %q, want cloudflare", got)
	}
	if got := CompanyFromSubject("unrelated subject"); got != "" {
		t.Errorf("CompanyFromSubject(unrelated) = %q, want empty", got)
	}
}

func TestKeyAgreesAcrossSides(t *testing.T) {
	// Submitter side derives company from the apply URL; webhook side
	// from the email subject. They must produce the same key.
	subURL := CompanyFromGreenhouseURL("https://boards.greenhouse.io/cloudflare/jobs/123")
	subMail := CompanyFromSubject("Security code for your application to Cloudflare")
	if Key("Joakim@Example.com ", subURL) != Key("joakim@example.com", subMail) {
		t.Fatalf("keys disagree: url=%q mail=%q", subURL, subMail)
	}
}
