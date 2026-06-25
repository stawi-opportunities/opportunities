package domain

import "time"

// Company is a record of an issuing organisation seen across opportunities:
// its display name, logo, website, and a rolling count of jobs it offers. The
// worker's CanonicalHandler upserts one per distinct issuer as opportunities
// are materialised, keyed by Slug (the normalised name). Embeds BaseModel so
// the table is created via GORM AutoMigrate like every other persisted model.
type Company struct {
	BaseModel
	Slug        string    `gorm:"column:slug;type:varchar(255);not null;uniqueIndex:idx_companies_slug" json:"slug"`
	Name        string    `gorm:"column:name;type:text;not null" json:"name"`
	LogoURL     *string   `gorm:"column:logo_url;type:text" json:"logo_url,omitempty"`
	Website     *string   `gorm:"column:website;type:text" json:"website,omitempty"`
	Country     *string   `gorm:"column:country;type:varchar(10)" json:"country,omitempty"`
	JobCount    int       `gorm:"column:job_count;not null;default:0" json:"job_count"`
	FirstSeenAt time.Time `gorm:"column:first_seen_at;not null;default:now()" json:"first_seen_at"`
	LastSeenAt  time.Time `gorm:"column:last_seen_at;not null;default:now()" json:"last_seen_at"`
}

func (Company) TableName() string { return "companies" }

// CompanySlug normalises a company name into a stable dedup key: lower-cased,
// legal-suffix-insensitive token form. Empty name → empty slug (skip).
func CompanySlug(name string) string {
	return NormalizeToken(name)
}
