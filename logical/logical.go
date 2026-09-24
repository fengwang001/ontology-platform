// Package logical joins physical lines of a java.util.Properties stream into
// logical lines (comment/blank skipping, backslash continuation, leading
// whitespace stripping on continued lines).
package logical

import (
	"bufio"
	"io"
)

// Line is one logical line. Text keeps escape sequences raw (no unescaping).
// Line is the 1-based physical line where the logical line begins.
// LeadSkip is the number of leading whitespace runes stripped from that first
// physical line (so callers can convert text columns to physical columns).
type Line struct {
	Text     string
	Line     int
	LeadSkip int
	Comment  bool
}

// Scanner yields logical lines from a properties stream.
type Scanner struct {
	r       *bufio.Reader
	line    int // current physical line number, 1-based
	skipLF  bool
	checked int64 // unexported: total bytes examined
}

// NewScanner creates a Scanner over r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReader(r), line: 1}
}

// Checks returns the total number of input bytes examined so far.
func (s *Scanner) Checks() int64 { return s.checked }

// Next returns the next logical line, or io.EOF when the stream is exhausted.
func (s *Scanner) Next() (Line, error) {
	var buf []byte
	startLine, leadSkip := 0, 0
	pending, leading, found, comment := false, true, false, false

	for {
		b, err := s.r.ReadByte()
		if err != nil {
			if err != io.EOF {
				return Line{}, err
			}
			if !found {
				return Line{}, io.EOF
			}
			break
		}
		s.checked++

		if s.skipLF {
			s.skipLF = false
			if b == '\n' {
				continue
			}
		}

		switch {
		case pending:
			pending = false
			if b == '\n' {
				s.line++
				leading = true
				continue
			}
			if b == '\r' {
				s.line++
				s.skipLF = true
				leading = true
				continue
			}
			buf = append(buf, '\\', b)
		case b == '\\':
			pending = true
			if leading {
				found, comment = true, false
			}
		case b == '\n', b == '\r':
			s.line++
			if b == '\r' {
				s.skipLF = true
			}
			if !found {
				// blank physical line: reset and keep reading
				startLine, leadSkip, buf = 0, 0, buf[:0]
				leading, comment = true, false
				continue
			}
			goto done
		case leading && (b == ' ' || b == '\t' || b == '\f'):
			if !found {
				leadSkip++
			}
		case leading && (b == '#' || b == '!'):
			if !found {
				found, comment = true, true
				startLine = s.line
			} else {
				buf = append(buf, b)
			}
			buf = append(buf, b)
			leading = false
		default:
			if !found {
				found = true
				startLine = s.line
				if pending {
					pending = false
					buf = append(buf, '\\')
				}
			}
			leading = false
			buf = append(buf, b)
		}
	}

done:
	_ = pending
	return Line{Text: string(buf), Line: startLine, LeadSkip: leadSkip, Comment: comment}, nil
}
