package history

import "errors"

// Error categories. Every rejected operation fails with one of these,
// checked in the order: invalid argument -> clock regression ->
// document not found -> invalid state. ErrSuperseded is only produced
// by traversal coalescing.
var (
	ErrInvalidArgument  = errors.New("history: invalid argument")
	ErrClockRegression  = errors.New("history: clock regression")
	ErrSuperseded       = errors.New("history: traversal superseded by a newer one")
	ErrDocumentNotFound = errors.New("history: document not found")
	ErrInvalidState     = errors.New("history: operation not allowed in this state")
)
