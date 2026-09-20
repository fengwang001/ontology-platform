// Package ctxtmpl implements a minimal context-aware template renderer.
//
// The only interpolation syntax is {{name}} where name matches
// [A-Za-z_][A-Za-z0-9_]*. Values are escaped according to the HTML
// context (text, quoted/unquoted attribute, URL attribute, comment)
// in which the interpolation point appears.
package ctxtmpl

import "errors"

// Sentinel errors returned by Render. Use errors.Is to test for them.
var (
	// ErrMissingKey is returned when the template references a key
	// that is not present in the data map.
	ErrMissingKey = errors.New("ctxtmpl: missing key")
	// ErrBadAction is returned for malformed {{...}} actions.
	ErrBadAction = errors.New("ctxtmpl: malformed action")
	// ErrAttrNamePosition is returned when an interpolation appears
	// inside a tag but outside any attribute value.
	ErrAttrNamePosition = errors.New("ctxtmpl: interpolation in attribute name position")
	// ErrUnclosedQuote is returned when the template ends inside a
	// quoted attribute value.
	ErrUnclosedQuote = errors.New("ctxtmpl: unclosed quote")
	// ErrUnclosedTag is returned when the template ends inside a tag.
	ErrUnclosedTag = errors.New("ctxtmpl: unclosed tag")
	// ErrUnclosedComment is returned when the template ends inside
	// an HTML comment.
	ErrUnclosedComment = errors.New("ctxtmpl: unclosed comment")
	// ErrDangerousURL is returned when a URL attribute value would
	// start with a dangerous scheme (javascript:, vbscript:, data:).
	ErrDangerousURL = errors.New("ctxtmpl: dangerous URL scheme")
)
