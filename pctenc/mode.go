// Package pctenc implements URL percent-encoding and decoding
// without relying on net/url.
package pctenc

import "errors"

// Mode selects the safe character set used by Encode and Decode.
type Mode int

const (
	// Path is for a single path segment.
	Path Mode = iota
	// Query is for a query key or value. Space encodes as %20, not "+".
	Query
	// Fragment is for the fragment component.
	Fragment
)

// ErrBadEscape is returned by Decode when a percent-escape sequence
// is malformed (e.g. "%", "%A", "%GG", "%2G").
var ErrBadEscape = errors.New("pctenc: invalid percent-escape sequence")
