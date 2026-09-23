// Package chunk splits a string into alternating ASCII-digit and
// non-digit runs. Only bytes '0'-'9' are treated as digits; every other
// byte (including UTF-8 continuation bytes) is ordinary text.
package chunk

// Kind identifies a segment's type.
type Kind uint8

const (
	Text  Kind = iota // non-digit run
	Digit             // ASCII '0'-'9' run
)

// Segment is one maximal run with its original text.
type Segment struct {
	Kind Kind
	Text string
}

// Split returns all segments of s in order.
func Split(s string) []Segment {
	var out []Segment
	for i := 0; i < len(s); {
		d := isDigit(s[i])
		j := i + 1
		for j < len(s) && isDigit(s[j]) == d {
			j++
		}
		k := Text
		if d {
			k = Digit
		}
		out = append(out, Segment{Kind: k, Text: s[i:j]})
		i = j
	}
	return out
}

// Scanner is a zero-allocation streaming view over a string.
type Scanner struct {
	s   string
	pos int
}

// NewScanner starts a scan at the beginning of s.
func NewScanner(s string) *Scanner { return &Scanner{s: s} }

// Done reports whether the whole string has been consumed.
func (sc *Scanner) Done() bool { return sc.pos >= len(sc.s) }

// Next returns the kind and text of the next maximal run and advances.
// Next must not be called when Done is true.
func (sc *Scanner) Next() (Kind, string) {
	d := isDigit(sc.s[sc.pos])
	j := sc.pos + 1
	for j < len(sc.s) && isDigit(sc.s[j]) == d {
		j++
	}
	text := sc.s[sc.pos:j]
	sc.pos = j
	if d {
		return Digit, text
	}
	return Text, text
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
