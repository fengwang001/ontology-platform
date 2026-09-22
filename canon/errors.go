// Package canon is the public URL canonicalizer and equivalence judge.
package canon

import "fmt"

// LimitKind classifies resource-limit rejections so callers can tell the
// three limits apart.
type LimitKind int

const (
	// LimitLength: the raw URL exceeds MaxURLLength.
	LimitLength LimitKind = iota
	// LimitSegments: the path has more than MaxPathSegments segments.
	LimitSegments
	// LimitParams: the query has more than MaxQueryParams items.
	LimitParams
)

func (k LimitKind) String() string {
	switch k {
	case LimitLength:
		return "url too long"
	case LimitSegments:
		return "too many path segments"
	case LimitParams:
		return "too many query params"
	}
	return "unknown limit"
}

// LimitError reports a rejected over-limit input. It carries no partial
// result and rejecting changes no state.
type LimitError struct {
	Kind  LimitKind
	Got   int
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("canon: %s: got %d, limit %d", e.Kind, e.Got, e.Limit)
}

// SyntaxError reports an unparseable URL shape (bad scheme, bad authority,
// fragment present, ...). Percent-encoding failures surface as *pct.Error.
type SyntaxError struct{ Msg string }

func (e *SyntaxError) Error() string { return "canon: syntax: " + e.Msg }
