package ctxtmpl

import "errors"

// Sentinel errors returned by Render. Use errors.Is to match them.
var (
	// ErrMissingKey is returned when the template references a key that is
	// absent from the data map.
	ErrMissingKey = errors.New("ctxtmpl: missing key")
	// ErrUnclosedAction is returned when an interpolation "{{" has no
	// matching "}}" before the end of the template.
	ErrUnclosedAction = errors.New("ctxtmpl: unclosed interpolation")
	// ErrDangerousProtocol is returned when the fully assembled value of a
	// URL attribute starts with a dangerous protocol scheme.
	ErrDangerousProtocol = errors.New("ctxtmpl: dangerous URL protocol")
)
