// Package rangespec parses an HTTP Range request header for the bytes unit.
//
// Only the three single-interval forms defined by RFC 7233 are accepted:
//
//	a-b   inclusive closed interval
//	a-    from a to the end
//	-n    the last n bytes
//
// The parser distinguishes two failure classes:
//
//   - *SyntaxError: the header is not well-formed; Offset gives the 0-based
//     byte offset of the first byte that could not be parsed.
//
// Unsatisfiability is deliberately not decided here: whether a syntactically
// valid range can be satisfied depends on the resource length and belongs to
// the coalesce package.
package rangespec

// SpecKind identifies which of the three syntactic forms a RangeSpec used.
type SpecKind int

const (
	// KindClosed is the "a-b" form (both endpoints present).
	KindClosed SpecKind = iota
	// KindOpenEnd is the "a-" form (from a to the end of the representation).
	KindOpenEnd
	// KindSuffix is the "-n" form (the final n bytes).
	KindSuffix
)

// RangeSpec is one parsed byte-range spec in its raw syntactic form.
//
// Field meaning by Kind:
//
//	KindClosed:   [Start, End] inclusive
//	KindOpenEnd:  Start..end-of-representation, End unused
//	KindSuffix:   the last Suffix bytes, Start/End unused
type RangeSpec struct {
	Kind   SpecKind
	Start  int64
	End    int64
	Suffix int64
}

// SyntaxError reports that the Range header violates the grammar. Offset is
// the 0-based byte index of the first byte that could not be parsed.
type SyntaxError struct {
	Offset int
	Reason string
}

func (e *SyntaxError) Error() string {
	return "rangespec: syntax error at byte " + itoa(e.Offset) + ": " + e.Reason
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
