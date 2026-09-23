// Package words splits POSIX shell style command lines into words
// (tokenizing and quote handling only) and quotes words back.
package words

import (
	"strings"

	"ontology/lex"
)

// Distinguishable tokenization errors, matched with errors.Is.
var (
	ErrUnterminatedSingle = lex.ErrSingle
	ErrUnterminatedDouble = lex.ErrDouble
	ErrTrailingBackslash  = lex.ErrEscape
)

// Splitter is a streaming word splitter. The zero value is ready to use.
type Splitter struct {
	lx    lex.Lexer
	ev    []lex.Event
	cur   []byte
	words []string
	err   error
}

// Feed consumes a chunk of input; chunks may split anywhere.
func (s *Splitter) Feed(p string) error {
	if s.err != nil {
		return s.err
	}
	for i := 0; i < len(p); i++ {
		s.ev = s.lx.Step(p[i], s.ev[:0])
		s.apply()
	}
	return nil
}

// Close finishes input and reports unterminated quotes or backslash.
func (s *Splitter) Close() error {
	if s.err != nil {
		return s.err
	}
	ev, err := s.lx.Close(s.ev[:0])
	s.ev = ev
	s.apply()
	s.err = err
	return err
}

func (s *Splitter) apply() {
	for _, e := range s.ev {
		switch e.Op {
		case lex.OpBegin:
			s.cur = s.cur[:0]
		case lex.OpEmit:
			s.cur = append(s.cur, e.Byte)
		case lex.OpEnd:
			s.words = append(s.words, string(s.cur))
		}
	}
}

// Words returns the words collected so far.
func (s *Splitter) Words() []string { return s.words }

// Split tokenizes s into words.
func Split(s string) ([]string, error) {
	var sp Splitter
	if err := sp.Feed(s); err != nil {
		return nil, err
	}
	if err := sp.Close(); err != nil {
		return nil, err
	}
	return sp.Words(), nil
}

// Quote renders words as one line that Split restores exactly.
// Every word is single-quoted; an embedded single quote is written
// as '\” (close, escaped quote, reopen) because single quotes
// cannot be escaped inside single quotes.
func Quote(ws []string) string {
	var b strings.Builder
	for i, w := range ws {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('\'')
		b.WriteString(strings.ReplaceAll(w, `'`, `'\''`))
		b.WriteByte('\'')
	}
	return b.String()
}
