// Package eol classifies line endings: CRLF, lone CR, and LF.
// A CR is pending until the next byte reveals whether it begins CRLF.
package eol

// Kind classifies a byte in the context of line endings.
type Kind uint8

const (
	Other   Kind = iota // not a line-ending byte
	LF                  // \n
	CR                  // \r, line end is not yet certain
)

// Classify reports whether b is CR, LF, or other.
func Classify(b byte) Kind {
	switch b {
	case '\r':
		return CR
	case '\n':
		return LF
	default:
		return Other
	}
}

// Token is one emitted unit: a confirmed line end or a run of other bytes.
type Token struct {
	LineEnd bool   // true: one logical line end; false: raw bytes in Data
	CRLF    bool   // when LineEnd, whether it consumed \r\n
	Data    []byte // when !LineEnd, the raw non-EOL bytes (shares input)
}

// Scanner turns a byte stream into Tokens, resolving the CR/CRLF ambiguity
// one byte later. Feed any chunk boundaries; identical input yields
// identical tokens regardless of where writes are split.
type Scanner struct {
	pendingCR bool
}

// NewScanner returns an empty Scanner.
func NewScanner() *Scanner { return &Scanner{} }

// Push appends a chunk and returns the tokens that are now determined.
func (s *Scanner) Push(p []byte) []Token {
	var out []Token
	start := 0
	flushOther := func(end int) {
		if end > start {
			out = append(out, Token{Data: p[start:end]})
		}
	}
	for i := 0; i < len(p); i++ {
		switch Classify(p[i]) {
		case CR:
			flushOther(i)
			if s.pendingCR {
				out = append(out, Token{LineEnd: true})
			}
			s.pendingCR = true
			start = i + 1
		case LF:
			if s.pendingCR {
				flushOther(i)
				out = append(out, Token{LineEnd: true, CRLF: true})
				s.pendingCR = false
				start = i + 1
			}
		}
	}
	flushOther(len(p))
	return out
}

// Finish resolves a trailing pending CR as a lone line end and reports
// whether one was emitted. It must be called once at stream end.
func (s *Scanner) Finish() (Token, bool) {
	if s.pendingCR {
		s.pendingCR = false
		return Token{LineEnd: true}, true
	}
	return Token{}, false
}
