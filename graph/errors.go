package graph

import "errors"

var (
	ErrStartNotFound       = errors.New("start object not found")
	ErrInvalidMaxDepth     = errors.New("max depth must be a positive integer")
	ErrInvalidResultLimit  = errors.New("result limit must be a positive integer")
	ErrEmptyLinkDirections = errors.New("link direction set must not be empty")
)
