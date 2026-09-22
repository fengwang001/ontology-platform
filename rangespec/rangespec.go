// Package rangespec parses the lexical and syntactic structure of an HTTP
// Range header ("bytes=a-b", "bytes=a-", "bytes=-n", comma-separated).
// It knows nothing about resource sizes; clipping and satisfiability are
// the job of the coalesce package.
package rangespec

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies which of the three Range header forms a Spec came from.
type Kind int

const (
	// Span is "a-b": bytes From through To, both ends inclusive.
	Span Kind = iota
	// Open is "a-": bytes From through the end of the resource.
	Open
	// Suffix is "-n": the last N bytes of the resource.
	Suffix
)

// Spec is one parsed byte-range request. Fields are raw syntactic values;
// they are not yet clipped against any resource size.
type Spec struct {
	Kind Kind
	From int64 // Span/Open: start offset (>= 0)
	To   int64 // Span: end offset, inclusive
	N    int64 // Suffix: requested trailing byte count (>= 0)
}

// SyntaxError reports a malformed Range header. Offset is the byte index
// within the original header string where parsing failed.
type SyntaxError struct {
	Offset int
	Reason string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("rangespec: syntax error at byte offset %d: %s", e.Offset, e.Reason)
}

const unitPrefix = "bytes="

// Parse parses a full Range header value such as "bytes=0-99, 200-, -50".
// It returns the specs in order of appearance, or a *SyntaxError.
func Parse(header string) ([]Spec, error) {
	if len(header) < len(unitPrefix) || !strings.EqualFold(header[:len(unitPrefix)], unitPrefix) {
		return nil, &SyntaxError{Offset: 0, Reason: `missing "bytes=" unit prefix`}
	}
	body := header[len(unitPrefix):]
	base := len(unitPrefix)
	var specs []Spec
	for {
		seg := body
		rest := ""
		comma := strings.IndexByte(body, ',')
		if comma >= 0 {
			seg = body[:comma]
			rest = body[comma+1:]
		}
		trimmed, tbase := trimOWS(seg, base)
		if trimmed == "" {
			return nil, &SyntaxError{Offset: base, Reason: "empty range spec"}
		}
		sp, err := parseSpec(trimmed, tbase)
		if err != nil {
			return nil, err
		}
		specs = append(specs, sp)
		if comma < 0 {
			break
		}
		base += comma + 1
		body = rest
	}
	return specs, nil
}

// trimOWS strips leading/trailing spaces and tabs, returning the trimmed
// segment and the offset of its first byte within the original header.
func trimOWS(s string, base int) (string, int) {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end], base + start
}

// parseSpec parses one "a-b", "a-" or "-n" segment. base is the offset of
// s[0] within the original header, used for error positions.
func parseSpec(s string, base int) (Spec, error) {
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		return Spec{}, &SyntaxError{Offset: base, Reason: "missing '-'"}
	}
	if dash == 0 {
		n, err := parseUint(s[1:], base+1)
		if err != nil {
			return Spec{}, err
		}
		return Spec{Kind: Suffix, N: n}, nil
	}
	a, err := parseUint(s[:dash], base)
	if err != nil {
		return Spec{}, err
	}
	if dash == len(s)-1 {
		return Spec{Kind: Open, From: a}, nil
	}
	b, err := parseUint(s[dash+1:], base+dash+1)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Kind: Span, From: a, To: b}, nil
}

// parseUint parses a non-empty run of decimal digits into an int64.
func parseUint(s string, base int) (int64, error) {
	if s == "" {
		return 0, &SyntaxError{Offset: base, Reason: "expected digits"}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, &SyntaxError{
				Offset: base + i,
				Reason: fmt.Sprintf("unexpected byte %q, expected digit", s[i]),
			}
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, &SyntaxError{Offset: base, Reason: "integer overflow"}
	}
	return v, nil
}
