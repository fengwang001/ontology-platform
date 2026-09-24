// Package rangespec parses a single HTTP Range request header for bytes.
//
// It depends on no other package of this module and performs no length
// resolution: the three syntactic forms ("a-b", "a-", "-n") are preserved as
// specs and only lexical/syntactic errors are reported here.
package rangespec

import "fmt"

// Kind identifies one of the three Range spec forms.
type Kind int

const (
	Closed  Kind = iota // a-b
	OpenEnd             // a-
	Suffix              // -n
)

// Spec is one comma-separated byte range as written by the client.
type Spec struct {
	Kind Kind
	A    int64 // closed/open-end: first byte; suffix: always 0
	B    int64 // closed: last byte; suffix: number of suffix bytes
}

// SyntaxError describes a malformed Range header. Offset is the byte index in
// the original header value where parsing failed.
type SyntaxError struct {
	Offset int
	Reason string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("rangespec: syntax error at offset %d: %s", e.Offset, e.Reason)
}

// Parse parses a "bytes=..." header value.
func Parse(header string) ([]Spec, error) {
	const pref = "bytes="
	if len(header) < len(pref) || header[:len(pref)] != pref {
		return nil, &SyntaxError{Offset: 0, Reason: `missing "bytes=" prefix`}
	}
	body := header[len(pref):]
	if len(body) == 0 {
		return nil, &SyntaxError{Offset: len(pref), Reason: "empty range list"}
	}
	var specs []Spec
	for start := 0; start <= len(body); {
		spec, next, err := parseElem(body, start)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
		if next == len(body) {
			return specs, nil
		}
		start = next + 1
	}
	return specs, nil
}

// parseElem parses body[start:] up to the next comma or end. next is the index
// just past the element (a comma position, or len(body)).
func parseElem(body string, start int) (spec Spec, next int, err error) {
	end := start
	for end < len(body) && body[end] != ',' {
		end++
	}
	tok := body[start:end]
	off := func(i int) int { return len("bytes=") + start + i }
	fail := func(i int, why string) (Spec, int, error) {
		return Spec{}, 0, &SyntaxError{Offset: off(i), Reason: why}
	}
	if len(tok) == 0 {
		return fail(0, "empty range spec")
	}
	dash := -1
	for i := 0; i < len(tok); i++ {
		if tok[i] == '-' {
			if dash >= 0 {
				return fail(i, "multiple '-' in range spec")
			}
			dash = i
		}
	}
	if dash < 0 {
		return fail(len(tok), "missing '-'")
	}
	left, right := tok[:dash], tok[dash+1:]
	if len(left) == 0 {
		if len(right) == 0 {
			return fail(dash, "range needs at least one number")
		}
		n, bad, ok := parseNum(right)
		if !ok {
			return fail(dash+1+bad, "invalid or overflowing suffix length")
		}
		spec = Spec{Kind: Suffix, B: n}
	} else {
		a, bad, ok := parseNum(left)
		if !ok {
			return fail(bad, "invalid range start")
		}
		if len(right) == 0 {
			spec = Spec{Kind: OpenEnd, A: a}
		} else {
			b, bad2, ok2 := parseNum(right)
			if !ok2 {
				return fail(dash+1+bad2, "invalid range end")
			}
			if b < a {
				return fail(dash+1, "last byte precedes first byte")
			}
			spec = Spec{Kind: Closed, A: a, B: b}
		}
	}
	return spec, end, nil
}

// parseNum parses one or more ASCII digits; bad is the index within s of the
// first non-digit (or of the digit that caused overflow).
func parseNum(s string) (n int64, bad int, ok bool) {
	const cutoff = (1<<63 - 1) / 10
	if len(s) == 0 {
		return 0, 0, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, i, false
		}
		d := int64(c - '0')
		if n > cutoff || (n == cutoff && d > (1<<63-1)%10) {
			return 0, i, false
		}
		n = n*10 + d
	}
	return n, 0, true
}
