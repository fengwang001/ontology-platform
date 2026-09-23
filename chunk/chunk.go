// Package chunk splits a string into an alternating stream of digit and
// non-digit chunks. Only ASCII '0'–'9' are treated as digits.
package chunk

// Scanner walks the chunks of a string without allocating a slice.
type Scanner struct {
	s    string
	pos  int
	text string
	dig  bool
}

// New returns a Scanner over s.
func New(s string) *Scanner { return &Scanner{s: s} }

// Next advances to the next chunk. It returns false when the string is
// exhausted.
func (n *Scanner) Next() bool {
	if n.pos >= len(n.s) {
		n.text, n.dig = "", false
		return false
	}
	start := n.pos
	dig := isDigit(n.s[start])
	n.pos++
	for n.pos < len(n.s) && isDigit(n.s[n.pos]) == dig {
		n.pos++
	}
	n.text, n.dig = n.s[start:n.pos], dig
	return true
}

// Text returns the original text of the current chunk.
func (n *Scanner) Text() string { return n.text }

// IsDigit reports whether the current chunk is an ASCII digit run.
func (n *Scanner) IsDigit() bool { return n.dig }

// Pos returns the number of source bytes consumed so far.
func (n *Scanner) Pos() int { return n.pos }

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
