package ontology

import (
	"fmt"
	"unicode/utf8"
)

// MaxScopeSize is the hard upper bound on the number of distinct object ids
// accepted by a single Extract call.
const MaxScopeSize = 10_000

// maxIDLen bounds the length of a well-formed object identifier.
const maxIDLen = 512

// validObjectID reports whether id is well formed. An object identifier is a
// non-empty UTF-8 string of at most 512 runes; it must not contain ASCII
// control characters or spaces.
func validObjectID(id ObjectID) bool {
	if len(id) == 0 || utf8.RuneCountInString(string(id)) > maxIDLen {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return false
		}
	}
	return true
}

// validTypeName reports whether a type name is well formed.
func validTypeName(name TypeName) bool {
	if len(name) == 0 || utf8.RuneCountInString(string(name)) > maxIDLen {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return false
		}
	}
	return true
}

// invalidArgumentf wraps ErrInvalidArgument with a descriptive message.
func invalidArgumentf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
}
