package models

import "errors"

var (
	ErrNotFound    = errors.New("ats: not found")
	ErrConflict    = errors.New("ats: conflict")
	ErrInvalid     = errors.New("ats: invalid")
	ErrForbidden   = errors.New("ats: forbidden")
	ErrEmptyAvail  = errors.New("ats: empty availability")
	ErrUnavailable = errors.New("ats: service unavailable")
)
