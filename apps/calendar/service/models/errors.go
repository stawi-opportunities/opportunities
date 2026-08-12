package models

import "errors"

var (
	ErrNotFound  = errors.New("calendar: not found")
	ErrInvalid   = errors.New("calendar: invalid")
	ErrConflict  = errors.New("calendar: conflict")
	ErrForbidden = errors.New("calendar: forbidden")
)
