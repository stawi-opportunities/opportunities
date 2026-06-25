package roamsubmitter

import "github.com/stawi-opportunities/opportunities/pkg/domain"

// Site captures the ONLY things that differ between ROAM Africa job
// boards. Everything else — session handling, enquiry-form scraping,
// CV/currency resolution, the profile-incomplete guard, multipart replay,
// and the enquiries-dashboard confirmation — is shared in engine.go.
//
// Add a new sister board by adding one constructor below; no engine
// changes are needed unless that board genuinely diverges from the shared
// Laravel platform.
type Site struct {
	name   string            // submitter identity / SubmitResult.Method
	source domain.SourceType // domain.SourceType handled
	origin string            // scheme+host; CanHandle prefix, Origin header
	//                          and the enquiries-dashboard URL all derive from it
}

// BrighterMonday is the Kenya board (brightermonday.co.ke).
func BrighterMonday() Site {
	return Site{
		name:   "brightermonday",
		source: domain.SourceBrighterMonday,
		origin: "https://www.brightermonday.co.ke",
	}
}

// Jobberman is the Nigeria board (jobberman.com).
func Jobberman() Site {
	return Site{
		name:   "jobberman",
		source: domain.SourceJobberman,
		origin: "https://www.jobberman.com",
	}
}
