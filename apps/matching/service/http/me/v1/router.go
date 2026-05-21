package v1

import (
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// Mount registers every Phase-4 extension-facing route under /api/me/*.
// Mutating routes are wrapped in Idempotency.
func Mount(mux *http.ServeMux, deps *Deps) {
	idem := func(group string, h http.Handler) http.Handler {
		return httpmw.Idempotency(httpmw.IdempotencyConfig{
			Store: deps.IdempotencyStore, RouteGroup: group,
		}, h)
	}
	auth := httpmw.CandidateAuth

	mux.Handle("GET /api/me", auth(meHandler(deps)))

	mux.Handle("GET /api/me/matches", auth(listMatches(deps)))
	mux.Handle("GET /api/me/matches/{match_id}", auth(getMatch(deps)))
	mux.Handle("POST /api/me/matches/{match_id}/dismiss",
		auth(idem("matches.dismiss", dismissMatch(deps))))
	mux.Handle("POST /api/me/matches/{match_id}/view", auth(viewMatch(deps)))

	mux.Handle("GET /api/me/rules", auth(getRules(deps)))
	mux.Handle("PUT /api/me/rules", auth(idem("rules.put", putRules(deps))))

	mux.Handle("GET /api/me/profile-fields", auth(profileFields(deps)))
}
