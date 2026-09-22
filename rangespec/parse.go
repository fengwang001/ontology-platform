package rangespec

import (
	"errors"
	"strconv"
	"strings"
)

// Parse parses the value of an HTTP Range header. Only the single
// byte-range unit is accepted, with one or more comma-separated specs.
//
// Parse performs lexical/syntactic work only: it never inspects a resource
// size, so it cannot tell an out-of-range request from a satisfiable one.
// A syntactically valid but unfulfillable request (for example "bytes=-0")
// parses without error; callers resolve specs with coalesce.Resolve.
func Parse(value string) ([]Spec, error) {
	const prefix = "bytes="
	if !strings.HasPrefix(value, prefix) {
		return nil, &SyntaxError{Offset: 0, Msg: `missing "bytes=" prefix`}
	}
	body := value[len(prefix):]
	if len(body) == 0 {
		return nil, &SyntaxError{Offset: len(prefix), Msg: "empty range list"}
	}

	var specs []Spec
	start := 0
	for start <= len(body) {
		for start < len(body) && body[start] == ' ' {
			start++
		}
		if start == len(body) {
			break
		}
		end := strings.IndexByte(body[start:], ',')
		if end < 0 {
			end = len(body) - start
		}
		token := body[start : start+end]
		spec, err := parseSpec(token, len(prefix)+start)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
		start += end + 1
	}
	if len(specs) == 0 {
		return nil, &SyntaxError{Offset: len(value), Msg: "no byte-range-spec found"}
	}
	return specs, nil
}

func parseSpec(token string, base int) (Spec, error) {
	dash := strings.IndexByte(token, '-')
	if dash < 0 {
		return Spec{}, &SyntaxError{Offset: base + len(token), Msg: "missing '-' in byte-range-spec"}
	}
	if strings.Count(token, "-") != 1 {
		return Spec{}, &SyntaxError{Offset: base + dash, Msg: "unexpected '-'"}
	}
	left := strings.TrimSpace(token[:dash])
	right := strings.TrimSpace(token[dash+1:])
	if len(left) != len(token[:dash]) || len(right) != len(token[dash+1:]) {
		return Spec{}, &SyntaxError{Offset: base + dash, Msg: "whitespace around '-' is not allowed"}
	}
	if left == "" {
		n, off, err := parseNonNeg(right, base+dash+1)
		if err != nil {
			return Spec{}, err
		}
		_ = off
		return Spec{Kind: Suffix, First: n}, nil
	}
	a, _, err := parseNonNeg(left, base)
	if err != nil {
		return Spec{}, err
	}
	if right == "" {
		return Spec{Kind: From, First: a}, nil
	}
	b, _, err := parseNonNeg(right, base+dash+1)
	if err != nil {
		return Spec{}, err
	}
	if b < a {
		return Spec{}, &SyntaxError{Offset: base + dash + 1, Msg: "last-byte-pos precedes first-byte-pos"}
	}
	return Spec{Kind: Closed, First: a, Last: b}, nil
}

func parseNonNeg(s string, base int) (int64, int, error) {
	if s == "" {
		return 0, base, &SyntaxError{Offset: base, Msg: "expected non-negative integer"}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, base + i, &SyntaxError{Offset: base + i, Msg: "expected digit"}
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, base, &SyntaxError{Offset: base, Msg: "integer out of range"}
	}
	return n, base + len(s), nil
}

// AsSyntaxError extracts a *SyntaxError from err, if that is what Parse
// returned.
func AsSyntaxError(err error) (*SyntaxError, bool) {
	var se *SyntaxError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}
