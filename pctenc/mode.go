// Package pctenc implements URL percent-encoding and decoding for
// individual components (path segments, query keys/values, fragments).
// It deliberately does not wrap net/url and does not implement form
// encoding: a literal '+' is never treated as a space.
package pctenc

// Mode selects which set of bytes is left unescaped.
type Mode int

const (
	// Path is for a single path segment.
	Path Mode = iota
	// Query is for a query key or value.
	Query
	// Fragment is for a fragment.
	Fragment
)
