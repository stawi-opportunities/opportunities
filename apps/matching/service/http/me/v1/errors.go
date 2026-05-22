package v1

import (
	"errors"
	"net/http"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// ProblemFromError maps matching's sentinel errors to problem+json
// responses.
func ProblemFromError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, matching.ErrNotFound):
		httpmw.ProblemJSON(w, http.StatusNotFound, "not_found", err.Error())
	default:
		httpmw.ProblemJSON(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
