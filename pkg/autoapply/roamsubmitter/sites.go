package roamsubmitter

import "github.com/stawi-opportunities/opportunities/pkg/domain"

// Site captures the ONLY things that differ between ROAM Africa job
// boards. Everything else — session handling, enquiry-form scraping,
// CV/currency resolution, the profile-incomplete guard, multipart replay,
// and the enquiries-dashboard confirmation — is shared in engine.go.
//
// The ROAM brands (BrighterMonday, Jobberman) run one platform per
// country, each on its own domain with its own session/cookies. Each
// country is therefore its own Site + source_type. Add a new sister
// board (or country) by adding one constructor below; no engine changes
// are needed unless that board genuinely diverges from the shared
// Laravel platform.
type Site struct {
	name   string            // submitter identity / SubmitResult.Method
	source domain.SourceType // domain.SourceType handled
	origin string            // scheme+host; CanHandle prefix, Origin header
	//                          and the enquiries-dashboard URL all derive from it
}

// BrighterMondayKE is the Kenya board (brightermonday.co.ke).
func BrighterMondayKE() Site {
	return Site{
		name:   "brightermonday",
		source: domain.SourceBrighterMonday,
		origin: "https://www.brightermonday.co.ke",
	}
}

// BrighterMondayUG is the Uganda board (brightermonday.co.ug).
func BrighterMondayUG() Site {
	return Site{
		name:   "brightermonday_ug",
		source: domain.SourceBrighterMondayUG,
		origin: "https://www.brightermonday.co.ug",
	}
}

// JobbermanNG is the Nigeria board (jobberman.com).
func JobbermanNG() Site {
	return Site{
		name:   "jobberman",
		source: domain.SourceJobberman,
		origin: "https://www.jobberman.com",
	}
}

// JobbermanGH is the Ghana board (jobberman.com.gh).
func JobbermanGH() Site {
	return Site{
		name:   "jobberman_gh",
		source: domain.SourceJobbermanGH,
		origin: "https://www.jobberman.com.gh",
	}
}

// AllSites returns every supported ROAM board, in registration order.
// Used by the autoapply wiring so adding a constructor above is the only
// step needed to enable a new board.
func AllSites() []Site {
	return []Site{
		BrighterMondayKE(),
		BrighterMondayUG(),
		JobbermanNG(),
		JobbermanGH(),
	}
}
