package domain

import "strings"

// JobSnapshot is the public JSON payload served from R2 at
// job-repo.stawi.org/jobs/<slug>.json. It is the wire contract consumed by the
// Hugo shell's client-side renderer. Kept intentionally flat and stable; bump
// SchemaVersion when breaking changes are introduced.
type JobSnapshot struct {
	SchemaVersion   int             `json:"schema_version"`
	ID              string          `json:"id"`
	Slug            string          `json:"slug"`
	Title           string          `json:"title"`
	Company         CompanyRef      `json:"company"`
	Category        string          `json:"category"`
	Location        LocationRef     `json:"location"`
	Employment      EmploymentRef   `json:"employment"`
	Compensation    CompensationRef `json:"compensation"`
	Skills          SkillsRef       `json:"skills"`
	DescriptionHTML string          `json:"description_html"`
	ApplyURL        string          `json:"apply_url,omitempty"`
	PostedAt        string          `json:"posted_at,omitempty"`
	UpdatedAt       string          `json:"updated_at,omitempty"`
	ExpiresAt       string          `json:"expires_at,omitempty"`
	QualityScore    float64         `json:"quality_score"`
	IsFeatured      bool            `json:"is_featured"`
	// Language is the ISO 639-1 code of the snapshot content itself. On
	// the primary jobs/{slug}.json it mirrors the source language; on
	// jobs/{slug}.{lang}.json the TranslateHandler overwrites it with
	// the target language so the UI can show a "translated from X" notice.
	Language string `json:"language,omitempty"`
}

type CompanyRef struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	LogoURL  string `json:"logo_url,omitempty"`
	Verified bool   `json:"verified"`
}

type LocationRef struct {
	Text       string `json:"text,omitempty"`
	RemoteType string `json:"remote_type,omitempty"`
	Country    string `json:"country,omitempty"`
}

type EmploymentRef struct {
	Type      string `json:"type,omitempty"`
	Seniority string `json:"seniority,omitempty"`
}

type CompensationRef struct {
	Min      float64 `json:"min,omitempty"`
	Max      float64 `json:"max,omitempty"`
	Currency string  `json:"currency,omitempty"`
	Period   string  `json:"period,omitempty"`
}

type SkillsRef struct {
	Required   []string `json:"required"`
	NiceToHave []string `json:"nice_to_have"`
}

const snapshotTimeLayout = "2006-01-02T15:04:05Z"

// BuildSnapshot turns a CanonicalJob into its public JobSnapshot using the
// job's description field as-is. Callers with a pre-sanitized HTML description
// should use BuildSnapshotWithHTML instead.
func BuildSnapshot(j *CanonicalJob) JobSnapshot {
	return buildSnapshot(j, j.Description)
}

// BuildSnapshotWithHTML is the sanitization-aware variant used by the publish
// pipeline (descriptionHTML has already passed through bluemonday).
func BuildSnapshotWithHTML(j *CanonicalJob, descriptionHTML string) JobSnapshot {
	return buildSnapshot(j, descriptionHTML)
}

func buildSnapshot(j *CanonicalJob, descHTML string) JobSnapshot {
	snap := JobSnapshot{
		SchemaVersion: 1,
		ID:            j.ID,
		Slug:          j.Slug,
		Title:         j.Title,
		Company: CompanyRef{
			Name: j.Company,
			Slug: Slugify(j.Company),
		},
		Category: j.Category,
		Location: LocationRef{
			Text:       j.LocationText,
			RemoteType: j.RemoteType,
			Country:    j.Country,
		},
		Employment: EmploymentRef{
			Type:      j.EmploymentType,
			Seniority: j.Seniority,
		},
		Compensation: CompensationRef{
			Min:      j.SalaryMin,
			Max:      j.SalaryMax,
			Currency: j.Currency,
			Period:   "year",
		},
		Skills: SkillsRef{
			Required:   splitCSV(j.RequiredSkills),
			NiceToHave: splitCSV(j.NiceToHaveSkills),
		},
		DescriptionHTML: descHTML,
		ApplyURL:        j.ApplyURL,
		QualityScore:    j.QualityScore,
		IsFeatured:      j.QualityScore >= 80,
		Language:        j.Language,
	}
	if snap.Category == "" {
		snap.Category = string(DeriveCategory(j.Roles, j.Industry))
	}
	if j.PostedAt != nil {
		snap.PostedAt = j.PostedAt.UTC().Format(snapshotTimeLayout)
	}
	if !j.UpdatedAt.IsZero() {
		snap.UpdatedAt = j.UpdatedAt.UTC().Format(snapshotTimeLayout)
	}
	if j.ExpiresAt != nil {
		snap.ExpiresAt = j.ExpiresAt.UTC().Format(snapshotTimeLayout)
	}
	return snap
}

// splitCSV splits a comma-separated string into trimmed non-empty entries.
// Returns an empty slice (not nil) when input is empty so JSON output is `[]`.
func splitCSV(csv string) []string {
	out := []string{}
	if csv == "" {
		return out
	}
	for _, part := range strings.Split(csv, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}
