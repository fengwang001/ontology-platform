// Package rangespec parses the lexical and syntactic structure of an HTTP
// Range header into a list of byte-range specs. It knows nothing about the
// resource length; clipping and satisfiability live in package coalesce.
package rangespec

import (
	"fmt"
	"strconv"
	"strings"
)

// Spec is one parsed byte-range spec, not yet clipped to a resource size.
//
// The three forms map onto the fields as follows:
//
//	"a-b" -> First=a, Last=b,   Suffix=-1
//	"a-"  -> First=a, Last=-1,  Suffix=-1
//	"-n"  -> First=0, Last=-1,  Suffix=n  (n may be 0; see coalesce)
type Spec struct {
	First  int64 // start offset for the "a-b" and "a-" forms
	Last   int64 // inclusive end for "a-b"; -1 means open-ended
	Suffix int64 // length for the "-n" form; -1 means not a suffix spec
}

// IsSuffix reports whether the spec came from the "-n" form.
func (s Spec) IsSuffix() bool { return s.Suffix >= 0 }

// SyntaxError reports a malformed Range header. Offset is the byte offset
// within the original header string where the problem was detected.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("rangespec: syntax error at byte %d: %s", e.Offset, e.Msg)
}

const prefix = "bytes="

// Parse splits a Range header like "bytes=0-99, 200-, -50" into specs.
// A syntactically valid but unsatisfiable spec (e.g. "-0") is NOT an error
// here; that judgment belongs to coalesce, which knows the resource size.
func Parse(header string) ([]Spec, error) {
	if !strings.HasPrefix(header, prefix) {
		return nil, &SyntaxError{Offset: 0, Msg: `missing "bytes=" prefix`}
	}
	rest := header[len(prefix):]
	base := len(prefix)
	if rest == "" {
		return nil, &SyntaxError{Offset: base, Msg: "empty range set"}
	}
	var specs []Spec
	i := 0
	for {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
		start := i
		for i < len(rest) && rest[i] != ',' {
			i++
		}
		end := i
		for end > start && (rest[end-1] == ' ' || rest[end-1] == '\t') {
			end--
		}
		item := rest[start:end]
		if item == "" {
			return nil, &SyntaxError{Offset: base + start, Msg: "empty range item"}
		}
		spec, perr := parseItem(item)
		if perr != nil {
			perr.Offset += base + start
			return nil, perr
		}
		specs = append(specs, spec)
		if i >= len(rest) {
			break
		}
		i++ // skip the comma
	}
	return specs, nil
}

// parseItem parses one "a-b", "a-" or "-n" item. Offsets in the returned
// error are relative to the start of the item.
func parseItem(s string) (Spec, *SyntaxError) {
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		return Spec{}, &SyntaxError{Offset: 0, Msg: "missing '-'"}
	}
	first, last := s[:dash], s[dash+1:]
	if first == "" {
		n, _, err := parseUint(last)
		if err != nil {
			err.Offset += dash + 1
			return Spec{}, err
		}
		return Spec{First: 0, Last: -1, Suffix: n}, nil
	}
	a, _, err := parseUint(first)
	if err != nil {
		return Spec{}, err
	}
	if last == "" {
		return Spec{First: a, Last: -1, Suffix: -1}, nil
	}
	b, _, err := parseUint(last)
	if err != nil {
		err.Offset += dash + 1
		return Spec{}, err
	}
	if b < a {
		return Spec{}, &SyntaxError{Offset: dash + 1, Msg: "range end before start"}
	}
	return Spec{First: a, Last: b, Suffix: -1}, nil
}

// parseUint parses a non-empty run of decimal digits.
func parseUint(s string) (int64, int, *SyntaxError) {
	if s == "" {
		return 0, 0, &SyntaxError{Offset: 0, Msg: "expected digits"}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, 0, &SyntaxError{Offset: i, Msg: "expected digits"}
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, 0, &SyntaxError{Offset: 0, Msg: "number out of range"}
	}
	return n, len(s), nil
}
