package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/RadhiFadlillah/whatlanggo"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/geocode"
)

// Normalizer holds optional collaborators (currently a geocoder) that
// enrich an ExternalOpportunity in-place before the variant is built.
// It is safe to leave the geocoder nil — Normalize falls back to the
// raw ExternalToVariant pipeline.
type Normalizer struct {
	geocoder *geocode.Geocoder
}

// New constructs a Normalizer. Pass nil for geocoder to skip
// gazetteer enrichment (useful in unit tests that don't care about
// coordinates).
func New(geocoder *geocode.Geocoder) *Normalizer {
	return &Normalizer{geocoder: geocoder}
}

// Normalize converts an ExternalOpportunity into a JobVariant. It
// mutates ext.AnchorLocation when the bundled gazetteer recognises
// the city (Lat/Lon and Region get filled in if blank), then
// delegates to ExternalToVariant for the existing field-level
// normalisation.
func (n *Normalizer) Normalize(ext *domain.ExternalOpportunity, sourceID, country, sourceBoard, language string, scrapedAt time.Time) JobVariant {
	if n != nil && n.geocoder != nil {
		n.geocoder.Enrich(ext)
	}
	return ExternalToVariant(*ext, sourceID, country, sourceBoard, language, scrapedAt)
}

// JobVariant is the normalised, pipeline-ready representation of one observed
// job posting. It is an in-memory-only struct — it is never persisted to
// Postgres directly. Downstream callers map its fields into event payloads
// (e.g. eventsv1.VariantIngestedV1) for the R2/event-log pipeline.
type JobVariant struct {
	ExternalJobID    string
	SourceID         string
	HardKey          string
	SourceURL        string
	ApplyURL         string
	Title            string
	Company          string
	LocationText     string
	Country          string
	Language         string
	RemoteType       string
	EmploymentType   string
	SalaryMin        float64
	SalaryMax        float64
	Currency         string
	Description      string
	Seniority        string
	Skills           string
	Roles            string
	Benefits         string
	ContactName      string
	ContactEmail     string
	Department       string
	Industry         string
	Education        string
	Experience       string
	Deadline         string
	UrgencyLevel     string
	UrgencySignals   string
	HiringTimeline   string
	InterviewStages  int
	HasTakeHome      bool
	FunnelComplexity string
	CompanySize      string
	FundingStage     string
	RequiredSkills   string
	NiceToHaveSkills string
	ToolsFrameworks  string
	GeoRestrictions  string
	TimezoneReq      string
	ApplicationType  string
	ATSPlatform      string
	RoleScope        string
	TeamSize         string
	ReportsTo        string
	PostedAt         *time.Time
	ScrapedAt        time.Time
	ContentHash      string
}

// Minimum description length, in runes, before we trust whatlanggo to
// override the source-declared language. Below this, the detection is noisy
// (especially on titles like "Senior Engineer"), so we stick with the
// source default.
const minLangDetectRunes = 200

// detectLanguage returns an ISO 639-1 code. It prefers whatlanggo's detection
// on long-enough text; otherwise it falls back to `fallback`, or "en" if the
// fallback is blank.
func detectLanguage(text, fallback string) string {
	fb := strings.ToLower(strings.TrimSpace(fallback))
	if fb == "" {
		fb = "en"
	}
	if len([]rune(text)) < minLangDetectRunes {
		return fb
	}
	info := whatlanggo.Detect(text)
	if !info.IsReliable() {
		return fb
	}
	if iso := whatlanggo.LangToStringShort(info.Lang); iso != "" {
		return iso
	}
	return fb
}

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

// ExternalToVariant converts a raw ExternalOpportunity into a normalised JobVariant
// ready for deduplication and storage.
//
// language is the ISO 639-1 code declared on the source (e.g. "en", "fr",
// "ja"). Callers that don't yet track language per source should pass "en".
// The normalizer will opportunistically override this with a whatlanggo
// detection when the description is long enough to give a reliable signal.
//
// Field mapping versus the old ExternalJob shape: job-specific scalars
// (RemoteType, EmploymentType, Seniority, etc.) and slices (Skills, Roles,
// Benefits, RequiredSkills, NiceToHaveSkills, ToolsFrameworks, ...) now live
// under ext.Attributes and are read via the AttrString / AttrStringSlice /
// AttrFloat helpers. This keeps the same JobVariant wire layout while the
// connector emits a kind-agnostic ExternalOpportunity.
func ExternalToVariant(ext domain.ExternalOpportunity, sourceID string, country, sourceBoard, language string, scrapedAt time.Time) JobVariant {
	// 1. Trim all text fields.
	title := strings.TrimSpace(ext.Title)
	company := strings.TrimSpace(ext.IssuingEntity)
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
	remoteType := strings.ToLower(ext.AttrString("remote_type"))
	if remoteType == "" && ext.Remote {
		remoteType = "remote"
	}
	employmentType := strings.ToLower(ext.AttrString("employment_type"))

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

	// 8b. Resolve language. Prefer a reliable whatlanggo detection when
	// description is long enough; otherwise inherit from source.
	lang := detectLanguage(description, language)

	// Serialize array fields to comma-separated strings for storage.
	skills := strings.Join(ext.AttrStringSlice("skills"), ", ")
	roles := strings.Join(ext.AttrStringSlice("roles"), ", ")
	benefits := strings.Join(ext.AttrStringSlice("benefits"), ", ")
	urgencySignals := strings.Join(ext.AttrStringSlice("urgency_signals"), ", ")
	requiredSkills := strings.Join(ext.AttrStringSlice("required_skills"), ", ")
	niceToHaveSkills := strings.Join(ext.AttrStringSlice("nice_to_have_skills"), ", ")
	toolsFrameworks := strings.Join(ext.AttrStringSlice("tools_frameworks"), ", ")

	// Deadline is now *time.Time on ExternalOpportunity. JobVariant still
	// carries a free-form string (RFC3339 if present, blank otherwise).
	deadlineStr := ""
	if ext.Deadline != nil && !ext.Deadline.IsZero() {
		deadlineStr = ext.Deadline.UTC().Format(time.RFC3339)
	}

	return JobVariant{
		ExternalJobID:    externalID,
		SourceID:         sourceID,
		HardKey:          hardKey,
		SourceURL:        sourceURL,
		ApplyURL:         applyURL,
		Title:            title,
		Company:          company,
		LocationText:     location,
		Country:          country,
		Language:         lang,
		RemoteType:       remoteType,
		EmploymentType:   employmentType,
		SalaryMin:        ext.AmountMin,
		SalaryMax:        ext.AmountMax,
		Currency:         currency,
		Description:      description,
		Seniority:        strings.ToLower(strings.TrimSpace(ext.AttrString("seniority"))),
		Skills:           skills,
		Roles:            roles,
		Benefits:         benefits,
		ContactName:      strings.TrimSpace(ext.AttrString("contact_name")),
		ContactEmail:     strings.TrimSpace(ext.AttrString("contact_email")),
		Department:       strings.TrimSpace(ext.AttrString("department")),
		Industry:         strings.TrimSpace(ext.AttrString("industry")),
		Education:        strings.TrimSpace(ext.AttrString("education")),
		Experience:       strings.TrimSpace(ext.AttrString("experience")),
		Deadline:         deadlineStr,
		UrgencyLevel:     strings.ToLower(strings.TrimSpace(ext.AttrString("urgency_level"))),
		UrgencySignals:   urgencySignals,
		HiringTimeline:   strings.ToLower(strings.TrimSpace(ext.AttrString("hiring_timeline"))),
		InterviewStages:  int(ext.AttrFloat("interview_stages")),
		HasTakeHome:      attrBool(ext, "has_take_home"),
		FunnelComplexity: strings.ToLower(strings.TrimSpace(ext.AttrString("funnel_complexity"))),
		CompanySize:      strings.ToLower(strings.TrimSpace(ext.AttrString("company_size"))),
		FundingStage:     strings.ToLower(strings.TrimSpace(ext.AttrString("funding_stage"))),
		RequiredSkills:   requiredSkills,
		NiceToHaveSkills: niceToHaveSkills,
		ToolsFrameworks:  toolsFrameworks,
		GeoRestrictions:  strings.ToLower(strings.TrimSpace(ext.AttrString("geo_restrictions"))),
		TimezoneReq:      strings.TrimSpace(ext.AttrString("timezone_req")),
		ApplicationType:  strings.ToLower(strings.TrimSpace(ext.AttrString("application_type"))),
		ATSPlatform:      strings.ToLower(strings.TrimSpace(ext.AttrString("ats_platform"))),
		RoleScope:        strings.ToLower(strings.TrimSpace(ext.AttrString("role_scope"))),
		TeamSize:         strings.ToLower(strings.TrimSpace(ext.AttrString("team_size"))),
		ReportsTo:        strings.TrimSpace(ext.AttrString("reports_to")),
		PostedAt:         ext.PostedAt,
		ScrapedAt:        scrapedAt,
		ContentHash:      hash,
	}
}

// attrBool reads a boolean attribute. Accepts native bool plus the string
// values commonly emitted by JSON encoders ("true"/"false").
func attrBool(ext domain.ExternalOpportunity, key string) bool {
	if ext.Attributes == nil {
		return false
	}
	switch v := ext.Attributes[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}
