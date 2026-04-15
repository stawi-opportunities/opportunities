package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"stawi.jobs/pkg/domain"
)

// companySuffixes is the ordered list of suffixes to strip from company names.
// Longer / more-specific entries must come before shorter ones so that
// "Pty. Ltd." is handled before "Ltd." etc.
var companySuffixes = []string{
	"Incorporated",
	"Limited",
	"Corp.",
	"Corp",
	"GmbH",
	"Inc.",
	"Inc",
	"Ltd.",
	"Ltd",
	"Pty.",
	"Pty",
	"PLC",
	"LLC",
}

// regionMap maps ISO-3166-1 alpha-2 country codes to region names.
var regionMap = map[string]string{
	// East Africa
	"KE": "east_africa", "UG": "east_africa", "TZ": "east_africa",
	"RW": "east_africa", "ET": "east_africa", "SO": "east_africa",
	// West Africa
	"NG": "west_africa", "GH": "west_africa", "SN": "west_africa",
	"CI": "west_africa", "CM": "west_africa", "ML": "west_africa",
	// Southern Africa
	"ZA": "southern_africa", "ZW": "southern_africa", "BW": "southern_africa",
	"MZ": "southern_africa", "ZM": "southern_africa", "MW": "southern_africa",
	"NA": "southern_africa",
	// North Africa
	"EG": "north_africa", "MA": "north_africa", "TN": "north_africa",
	"DZ": "north_africa", "LY": "north_africa",
	// Europe
	"GB": "europe", "DE": "europe", "FR": "europe", "NL": "europe",
	"IE": "europe", "ES": "europe", "IT": "europe", "SE": "europe",
	"NO": "europe", "DK": "europe", "FI": "europe", "PL": "europe",
	"PT": "europe", "AT": "europe", "CH": "europe", "BE": "europe",
	// Oceania
	"AU": "oceania", "NZ": "oceania",
	// Americas
	"US": "americas", "CA": "americas", "BR": "americas", "MX": "americas",
	// Asia
	"IN": "asia", "SG": "asia", "JP": "asia", "CN": "asia",
	"AE": "asia", "IL": "asia",
}

// DetectRegion returns the region name for the given upper-case country code,
// or an empty string if the country is not mapped.
func DetectRegion(country string) string {
	return regionMap[strings.ToUpper(country)]
}

// normalizeCompany strips known legal suffixes from a company name and
// collapses surrounding whitespace.
func normalizeCompany(name string) string {
	name = strings.TrimSpace(name)
	for _, suffix := range companySuffixes {
		// Match suffix at end of string, optionally preceded by a comma or space.
		for _, sep := range []string{", " + suffix, " " + suffix} {
			if strings.HasSuffix(name, sep) {
				name = strings.TrimSpace(name[:len(name)-len(sep)])
				break
			}
		}
	}
	// Collapse internal whitespace
	return strings.Join(strings.Fields(name), " ")
}

// sanitizeDescription removes null bytes and collapses runs of whitespace
// (spaces, tabs, newlines) into single spaces, then trims.
func sanitizeDescription(s string) string {
	// Remove null bytes
	s = strings.ReplaceAll(s, "\x00", "")
	// Collapse whitespace runs to a single space
	return strings.Join(strings.Fields(s), " ")
}

// contentHash computes a SHA-256 hex digest over the canonical identity fields.
func contentHash(externalID, title, company, location, description string) string {
	raw := fmt.Sprintf("%s|%s|%s|%s|%s", externalID, title, company, location, description)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// ExternalToVariant converts a raw ExternalJob into a normalised JobVariant
// ready for deduplication and storage.
func ExternalToVariant(ext domain.ExternalJob, sourceID int64, country, sourceBoard string, scrapedAt time.Time) domain.JobVariant {
	// 1. Trim all text fields.
	title := strings.TrimSpace(ext.Title)
	company := strings.TrimSpace(ext.Company)
	location := strings.TrimSpace(ext.LocationText)
	description := strings.TrimSpace(ext.Description)
	applyURL := strings.TrimSpace(ext.ApplyURL)
	sourceURL := strings.TrimSpace(ext.SourceURL)
	externalID := strings.TrimSpace(ext.ExternalID)

	// 2. Sanitize description.
	description = sanitizeDescription(description)

	// 3. Normalize company name.
	company = normalizeCompany(company)

	// 4. Case normalization.
	country = strings.ToUpper(strings.TrimSpace(country))
	currency := strings.ToUpper(ext.Currency)
	remoteType := strings.ToLower(ext.RemoteType)
	employmentType := strings.ToLower(ext.EmploymentType)

	// 5. Compute SHA-256 content hash.
	hash := contentHash(externalID, title, company, location, description)

	// 6. Generate externalID from first 16 chars of hash if missing.
	if externalID == "" {
		externalID = hash[:16]
	}

	// 7. Compute hard key.
	hardKey := domain.BuildHardKey(company, title, location, externalID)

	// 8. Assign region.
	_ = sourceBoard // sourceBoard is stored on the source record; passed here for future use
	region := DetectRegion(country)
	_ = region // region will be used once JobVariant has a Region field

	// Serialize array fields to comma-separated strings for storage.
	skills := strings.Join(ext.Skills, ", ")
	roles := strings.Join(ext.Roles, ", ")
	benefits := strings.Join(ext.Benefits, ", ")

	return domain.JobVariant{
		ExternalJobID:  externalID,
		SourceID:       sourceID,
		HardKey:        hardKey,
		SourceURL:      sourceURL,
		ApplyURL:       applyURL,
		Title:          title,
		Company:        company,
		LocationText:   location,
		Country:        country,
		RemoteType:     remoteType,
		EmploymentType: employmentType,
		SalaryMin:      ext.SalaryMin,
		SalaryMax:      ext.SalaryMax,
		Currency:       currency,
		Description:    description,
		Seniority:      strings.ToLower(strings.TrimSpace(ext.Seniority)),
		Skills:         skills,
		Roles:          roles,
		Benefits:       benefits,
		ContactName:    strings.TrimSpace(ext.ContactName),
		ContactEmail:   strings.TrimSpace(ext.ContactEmail),
		Department:     strings.TrimSpace(ext.Department),
		Industry:       strings.TrimSpace(ext.Industry),
		Education:      strings.TrimSpace(ext.Education),
		Experience:     strings.TrimSpace(ext.Experience),
		Deadline:       strings.TrimSpace(ext.Deadline),
		PostedAt:       ext.PostedAt,
		ScrapedAt:      scrapedAt,
		ContentHash:    hash,
	}
}
