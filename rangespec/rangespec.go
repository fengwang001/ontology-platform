// Package rangespec parses an HTTP Range header value into byte-range specs.
//
// It performs only lexical and syntactic analysis: the three supported forms
// are "a-b" (closed), "a-" (open-ended) and "-n" (suffix). Whether a spec is
// satisfiable for a resource of a given length is decided elsewhere, because
// that requires information (the resource length) this package does not have.
package rangespec

import (
	"errors"
	"strconv"
	"strings"
)

// Kind identifies one of the three byte-range syntax forms.
type Kind int

const (
	// Closed is "a-b" and selects bytes [First, Last].
	Closed Kind = iota
	// From is "a-" and selects bytes [First, end of resource].
	From
	// Suffix is "-n" and selects the final N bytes of the resource.
	Suffix
)

// Spec is one syntactically valid byte-range expression.
type Spec struct {
	Kind  Kind
	First int64 // used by Closed and From
	Last  int64 // used by Closed
	N     int64 // used by Suffix
}

// SyntaxError reports a lexical/grammatical failure. Offset is the byte
// position within the trimmed header value at which parsing failed.
type SyntaxError struct {
	Offset int64
	Msg    string
}

func (e *SyntaxError) Error() string {
	return "range syntax error at byte " + strconv.FormatInt(e.Offset, 10) + ": " + e.Msg
}

// IsSyntaxError reports whether err is a *SyntaxError.
func IsSyntaxError(err error) bool {
	var se *SyntaxError
	return errors.As(err, &se)
}

// Parse decodes a Range header value such as "bytes=0-499,-100,500-".
// Only the "bytes" unit is accepted. Surrounding OWS is tolerated; no other
// whitespace is legal.
func Parse(header string) ([]Spec, error) {
	v := strings.TrimSpace(header)
	if len(v) < len("bytes=") || v[:len("bytes=")] != "bytes=" {
		return nil, &SyntaxError{Offset: 0, Msg: `expected "bytes=" prefix`}
	}
	body := v[len("bytes="):]
	base := int64(len("bytes="))
	if body == "" {
		return nil, &SyntaxError{Offset: base, Msg: "empty range list"}
	}

	var specs []Spec
	start := 0
	for start < len(body) {
		// Find the end of this comma-delimited segment.
		end := strings.IndexByte(body[start:], ',')
		if end < 0 {
			end = len(body) - start
		}
		seg := body[start : start+end]
		spec, err := parseSegment(seg, base+int64(start))
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
		start += end
		if start < len(body) {
			start++ // consume the comma
		}
	}
	return specs, nil
}

func parseSegment(seg string, base int64) (Spec, error) {
	if seg == "" {
		return Spec{}, &SyntaxError{Offset: base, Msg: "empty range segment"}
	}
	dash := strings.IndexByte(seg, '-')
	if dash < 0 {
		return Spec{}, &SyntaxError{Offset: base, Msg: `missing "-"`}
	}
	if strings.IndexByte(seg[dash+1:], '-') >= 0 {
		return Spec{}, &SyntaxError{Offset: base + int64(dash+1) + int64(strings.IndexByte(seg[dash+1:], '-')), Msg: `unexpected "-"`}
	}
	left, right := seg[:dash], seg[dash+1:]

	if left == "" {
		// Suffix form "-n".
		if right == "" {
			return Spec{}, &SyntaxError{Offset: base, Msg: `expected digit after "-"`}
		}
		n, err := parseNonNeg(right, base+1)
		if err != nil {
			return Spec{}, err
		}
		return Spec{Kind: Suffix, N: n}, nil
	}

	first, err := parseNonNeg(left, base)
	if err != nil {
		return Spec{}, err
	}
	if right == "" {
		return Spec{Kind: From, First: first}, nil
	}
	last, err := parseNonNeg(right, base+int64(dash)+1)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Kind: Closed, First: first, Last: last}, nil
}

func parseNonNeg(s string, off int64) (int64, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, &SyntaxError{Offset: off + int64(i), Msg: "expected decimal digit"}
		}
	}
	n, convErr := strconv.ParseInt(s, 10, 64)
	if convErr != nil {
		return 0, &SyntaxError{Offset: off, Msg: "integer out of range"}
	}
	return n, nil
}
