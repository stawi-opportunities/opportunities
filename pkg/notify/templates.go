package notify

// Templates holds config-driven service-notification template names.
// Same idea as profile's MessageTemplateContactVerification env field.
type Templates struct {
	MatchesReady     string
	MatchesDigest    string
	WeeklyJobsDigest string
	CVStaleNudge     string
}

// Ready returns the matches-ready template (every-match alert).
func (t Templates) Ready() string {
	return OrDefault(t.MatchesReady, DefaultTemplateMatchesReady)
}

// Digest returns the paid-subscriber match digest template.
func (t Templates) Digest() string {
	return OrDefault(t.MatchesDigest, DefaultTemplateMatchesDigest)
}

// WeeklyJobs returns the unpaid re-engagement digest template.
func (t Templates) WeeklyJobs() string {
	return OrDefault(t.WeeklyJobsDigest, DefaultTemplateWeeklyJobsDigest)
}

// CVStale returns the CV refresh nudge template.
func (t Templates) CVStale() string {
	return OrDefault(t.CVStaleNudge, DefaultTemplateCVStaleNudge)
}
