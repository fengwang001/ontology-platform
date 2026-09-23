// Package words splits a command line into POSIX-shell-style words and
// quotes words back into a form Split restores exactly. Only quoting is
// handled: there is no expansion, globbing, substitution or redirection.
package words

import (
	"strings"

	"ontology/lex"
)

// Splitter is a streaming tokenizer fed in arbitrary byte chunks.
type Splitter struct {
	m      lex.Machine
	words  []string
	cur    strings.Builder
	open   bool
	closed bool
	err    error
}

func (s *Splitter) apply(ev lex.Event) {
	switch ev.Kind {
	case lex.Start:
		s.open = true
	case lex.Byte:
		s.cur.WriteByte(ev.Byte)
	case lex.End:
		s.words = append(s.words, s.cur.String())
		s.cur.Reset()
		s.open = false
	}
}

func (s *Splitter) pump() {
	for _, ev := range s.m.Events() {
		s.apply(ev)
	}
}

// Feed accepts one arbitrary chunk of input. It may be called any number of
// times, including one byte at a time; chunk boundaries never change output.
func (s *Splitter) Feed(chunk string) {
	for i := 0; i < len(chunk); i++ {
		s.m.Step(chunk[i], s.m.Processed())
		s.pump()
	}
}

// Close marks the end of input and records any trailing error.
func (s *Splitter) Close() error {
	if s.closed {
		return s.err
	}
	s.closed = true
	for _, ev := range s.m.End() {
		s.apply(ev)
	}
	s.err = s.m.Err()
	return s.err
}

// Words returns the words completed so far. After a nil error from Close it
// holds the complete result.
func (s *Splitter) Words() []string { return s.words }

// Processed reports the total bytes consumed by the underlying lexer.
func (s *Splitter) Processed() int { return s.m.Processed() }

// Split tokenizes a whole string. It returns the words and any unclosed
// quote or trailing-backslash error.
func Split(s string) ([]string, error) {
	var z Splitter
	z.Feed(s)
	if err := z.Close(); err != nil {
		return z.Words(), err
	}
	return z.Words(), nil
}

// Quote renders w as a single line that Split restores verbatim. Every word
// is wrapped in single quotes; each embedded ' is written as '\” (close,
// an escaped ', reopen), because a single quote cannot be escaped while
// inside a single-quoted region.
func Quote(w []string) string {
	parts := make([]string, len(w))
	for i, word := range w {
		var b strings.Builder
		b.Grow(len(word) + 2)
		b.WriteByte('\'')
		for j := 0; j < len(word); j++ {
			if word[j] == '\'' {
				b.WriteString(`'\''`)
				continue
			}
			b.WriteByte(word[j])
		}
		b.WriteByte('\'')
		parts[i] = b.String()
	}
	return strings.Join(parts, " ")
}
