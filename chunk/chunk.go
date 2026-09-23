// Package chunk splits a string into alternating numeric and non-numeric runs.
// Only ASCII bytes '0'..'9' are treated as digits.
package chunk

// Kind identifies a run.
type Kind uint8

const (
	Done  Kind = iota // no more runs
	Digit             // run of ASCII digits
	Other             // run of non-digit bytes
)

// Scanner walks the runs of a string left to right without allocating slices.
type Scanner struct {
	s    string
	pos  int
	kind Kind
	lo   int
	hi   int
}

// NewScanner returns a scanner positioned before the first run of s.
func NewScanner(s string) *Scanner {
	return &Scanner{s: s}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Next advances to the next run and reports whether one exists.
func (s *Scanner) Next() bool {
	if s.pos >= len(s.s) {
		s.kind, s.lo, s.hi = Done, s.pos, s.pos
		return false
	}
	s.lo = s.pos
	digit := isDigit(s.s[s.pos])
	if digit {
		s.kind = Digit
	} else {
		s.kind = Other
	}
	for s.pos < len(s.s) && isDigit(s.s[s.pos]) == digit {
		s.pos++
	}
	s.hi = s.pos
	return true
}

// Kind returns the kind of the current run, or Done after the last run.
func (s *Scanner) Kind() Kind { return s.kind }

// Pos reports how many bytes of the string have been scanned so far.
func (s *Scanner) Pos() int { return s.pos }

// Text returns the original text of the current run.
func (s *Scanner) Text() string { return s.s[s.lo:s.hi] }

// Run is one maximal digit or non-digit run with its original text.
type Run struct {
	Digit bool
	Text  string
}

// Split returns every maximal run of s in order.
func Split(s string) []Run {
	runs := make([]Run, 0, 1)
	sc := NewScanner(s)
	for sc.Next() {
		runs = append(runs, Run{Digit: sc.Kind() == Digit, Text: sc.Text()})
	}
	return runs
}
