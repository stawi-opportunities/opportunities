package handlers

import (
	"errors"

	"connectrpc.com/connect"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

func mapConnectErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, models.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, models.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, models.ErrInvalid), errors.Is(err, models.ErrEmptyAvail):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, models.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
