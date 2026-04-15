package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"stawi.jobs/internal/domain"
)

func ExternalToVariant(ext domain.ExternalJob, sourceID int64, country string, scrapedAt time.Time) domain.JobVariant {
	description := sanitize(ext.Description)
	contentHash := hashString(strings.Join([]string{
		ext.ExternalID,
		ext.Title,
		ext.Company,
		ext.LocationText,
		description,
	}, "|"))
	if ext.ExternalID == "" {
		ext.ExternalID = contentHash[:16]
	}
	return domain.JobVariant{
		ExternalJobID:  ext.ExternalID,
		SourceID:       sourceID,
		SourceURL:      ext.SourceURL,
		ApplyURL:       ext.ApplyURL,
		Title:          strings.TrimSpace(ext.Title),
		Company:        strings.TrimSpace(ext.Company),
		LocationText:   strings.TrimSpace(ext.LocationText),
		Country:        strings.ToUpper(strings.TrimSpace(country)),
		RemoteType:     strings.ToLower(strings.TrimSpace(ext.RemoteType)),
		EmploymentType: strings.ToLower(strings.TrimSpace(ext.EmploymentType)),
		SalaryMin:      ext.SalaryMin,
		SalaryMax:      ext.SalaryMax,
		Currency:       strings.ToUpper(strings.TrimSpace(ext.Currency)),
		Description:    description,
		PostedAt:       ext.PostedAt,
		ScrapedAt:      scrapedAt,
		ContentHash:    contentHash,
	}
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\u0000", "")
	s = strings.TrimSpace(s)
	return strings.Join(strings.Fields(s), " ")
}

func hashString(in string) string {
	h := sha256.Sum256([]byte(in))
	return hex.EncodeToString(h[:])
}
