package otprendezvous

import (
	"net/url"
	"regexp"
	"strings"
)

// Key derives the rendezvous key for an application from the candidate's
// email and the company the job is hosted under. Both sides of the
// rendezvous — the submitter (company from the apply URL) and the email
// webhook (company from the mail subject) — must agree on this key, so
// both inputs are normalised the same way.
func Key(email, company string) string {
	return normEmail(email) + "|" + NormCompany(company)
}

func normEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

// companySuffixes are dropped from a company name before comparison so
// "Cloudflare, Inc." (mail subject) and "cloudflare" (board URL token)
// collapse to the same token.
var companySuffixes = map[string]bool{
	"inc": true, "incorporated": true, "llc": true, "ltd": true,
	"limited": true, "corp": true, "corporation": true, "co": true,
	"gmbh": true, "plc": true,
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// NormCompany lowercases, strips punctuation and trailing legal suffixes,
// then removes spaces so a board URL token and a subject display name
// reconcile to the same string.
func NormCompany(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, " ")
	fields := strings.Fields(s)
	for len(fields) > 0 && companySuffixes[fields[len(fields)-1]] {
		fields = fields[:len(fields)-1]
	}
	return strings.Join(fields, "")
}

// CompanyFromGreenhouseURL extracts the board company token from a
// Greenhouse apply URL. Handles:
//
//	https://boards.greenhouse.io/<token>/jobs/<id>
//	https://job-boards.greenhouse.io/<token>/jobs/<id>
//	https://boards.greenhouse.io/embed/job_app?for=<token>&token=<id>
//
// Returns "" when no token can be found (e.g. a company-proxied apply
// URL), in which case the caller leaves the OTP phase disabled for that
// application.
func CompanyFromGreenhouseURL(applyURL string) string {
	u, err := url.Parse(applyURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 0 && parts[0] != "" && parts[0] != "embed" {
		return NormCompany(parts[0])
	}
	if forTok := u.Query().Get("for"); forTok != "" {
		return NormCompany(forTok)
	}
	return ""
}

// subjectCompanyRE captures the company name from the Greenhouse OTP
// subject line, e.g. "Security code for your application to Cloudflare".
var subjectCompanyRE = regexp.MustCompile(`(?i)application to (.+?)\s*$`)

// CompanyFromSubject pulls the company name out of the OTP email subject.
// Returns "" when the subject doesn't match the expected shape.
func CompanyFromSubject(subject string) string {
	m := subjectCompanyRE.FindStringSubmatch(strings.TrimSpace(subject))
	if len(m) < 2 {
		return ""
	}
	return NormCompany(m[1])
}
