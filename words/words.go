// Package words splits shell-style command lines into words and quotes
// word lists back into splittable lines.
package words

import (
	"strings"

	"ontology/lex"
)

// Split tokenizes s into words following POSIX quoting rules.
func Split(s string) ([]string, error) {
	sp := NewSplitter()
	sp.Feed(s)
	if err := sp.Close(); err != nil {
		return nil, err
	}
	return sp.Words(), nil
}

// Splitter is a streaming word splitter: Feed any chunking, then Close.
type Splitter struct {
	lx    *lex.Lexer
	words []string
}

// NewSplitter returns a ready-to-use Splitter.
func NewSplitter() *Splitter {
	s := &Splitter{}
	s.lx = lex.New(func(w string) { s.words = append(s.words, w) })
	return s
}

// Feed processes the next chunk of input.
func (s *Splitter) Feed(p string) { s.lx.Feed([]byte(p)) }

// Close finalizes the stream and reports unterminated constructs.
func (s *Splitter) Close() error { return s.lx.End() }

// Words returns the words completed so far.
func (s *Splitter) Words() []string { return s.words }

// Quote renders words as one line that Split restores to the identical list.
// Every word is single-quoted; an embedded single quote is written as the
// classic '\” sequence because single quotes cannot be escaped inside
// single quotes.
func Quote(ws []string) string {
	var b strings.Builder
	for i, w := range ws {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('\'')
		for j := 0; j < len(w); j++ {
			if w[j] == '\'' {
				b.WriteString(`'\''`)
			} else {
				b.WriteByte(w[j])
			}
		}
		b.WriteByte('\'')
	}
	return b.String()
}
