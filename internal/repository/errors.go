package repository

import "errors"

var (
	ErrNotFound = errors.New("repository object not found")
	ErrConflict = errors.New("repository object conflicts with existing data")
)
