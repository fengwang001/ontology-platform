package rangespec

import "strconv"

// Kind identifies the spelling of a single byte-range-spec.
type Kind int

const (
	// Closed is "a-b": both endpoints inclusive.
	Closed Kind = iota
	// From is "a-": from byte a to the end of the representation.
	From
	// Suffix is "-n": the final n bytes.
	Suffix
)

// Spec is one raw range-spec exactly as written in the Range header value
// (after stripping the "bytes=" prefix). Values are not yet resolved against a
// resource size; callers use coalesce.Resolve for that.
type Spec struct {
	Kind Kind
	// First is the first-byte-pos for Closed/From, or the suffix length for
	// Suffix. Last is the last-byte-pos only for Closed.
	First int64
	Last  int64
}

// SyntaxError reports a malformed Range header value. Offset is the zero-based
// byte offset (relative to the value passed to Parse, including the "bytes="
// prefix) of the first offending byte.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return "invalid Range header at offset " + strconv.Itoa(e.Offset) + ": " + e.Msg
}
