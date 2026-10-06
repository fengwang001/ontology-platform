package pretty

import "errors"

// Distinguishable error kinds returned by Session.Render and
// Session.Register. Use errors.Is to match them.
var (
	// ErrInvalidArgument: line width outside [1, 10000], negative indent
	// increment, text containing a newline, or empty fragment name.
	ErrInvalidArgument = errors.New("pretty: invalid argument")
	// ErrTooDeep: nesting depth after fragment expansion exceeds 1000.
	ErrTooDeep = errors.New("pretty: document too deep")
	// ErrUnregisteredRef: the document references an unregistered fragment.
	ErrUnregisteredRef = errors.New("pretty: unregistered fragment reference")
	// ErrOutputTooLarge: total width of the rendered result exceeds 1e7.
	ErrOutputTooLarge = errors.New("pretty: output too large")
	// ErrDuplicateName: a fragment with the same name is already registered.
	ErrDuplicateName = errors.New("pretty: duplicate fragment name")
)
