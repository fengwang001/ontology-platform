// Package words splits POSIX shell-style command lines into words.
package words

import (
	"strings"

	"ontology/lex"
)

// Split tokenizes a complete command line.
func Split(s string) ([]string, error) {
	t := NewSplitter()
	if err := t.Feed([]byte(s)); err != nil {
		return nil, err
	}
	if err := t.Close(); err != nil {
		return nil, err
	}
	return t.Words(), nil
}

// Splitter is a streaming tokenizer fed incrementally with Feed.
type Splitter struct {
	mach   *lex.Machine
	out    []string
	cur    strings.Builder
	inWord bool
}

// NewSplitter creates a streaming tokenizer.
func NewSplitter() *Splitter {
	s := &Splitter{}
	s.mach = lex.New(s.onEvent)
	return s
}

func (s *Splitter) onEvent(e lex.Event) {
	switch e.Kind {
	case lex.EvByte:
		s.inWord = true
		s.cur.WriteByte(e.Byte)
	case lex.EvMark:
		s.inWord = true
	case lex.EvSep:
		if s.inWord {
			s.out = append(s.out, s.cur.String())
			s.cur.Reset()
			s.inWord = false
		}
	}
}

// Feed pushes one chunk of input through the tokenizer.
func (s *Splitter) Feed(p []byte) error {
	return s.mach.Feed(p)
}

// Close flushes buffered input and returns an error if the line is incomplete.
func (s *Splitter) Close() error {
	if err := s.mach.Close(); err != nil {
		return err
	}
	if s.inWord {
		s.out = append(s.out, s.cur.String())
		s.cur.Reset()
		s.inWord = false
	}
	return nil
}

// Words returns the fully accumulated words after Close.
func (s *Splitter) Words() []string {
	return s.out
}

// Quote renders words as one line that Split recovers exactly.
func Quote(w []string) string {
	parts := make([]string, len(w))
	for i, word := range w {
		parts[i] = quoteOne(word)
	}
	return strings.Join(parts, " ")
}

func quoteOne(word string) string {
	if word == "" {
		return "''"
	}
	if allSafe(word) {
		return word
	}
	// Single quotes make every byte literal; close-escape-reopen for a
	// literal single quote because no escape exists inside single quotes.
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

func allSafe(word string) bool {
	for i := 0; i < len(word); i++ {
		if !safeByte[word[i]] {
			return false
		}
	}
	return true
}

var safeByte = func() [256]bool {
	var b [256]bool
	for c := 'a'; c <= 'z'; c++ {
		b[c] = true
	}
	for c := 'A'; c <= 'Z'; c++ {
		b[c] = true
	}
	for c := '0'; c <= '9'; c++ {
		b[c] = true
	}
	for _, c := range "-_.,/@%+:=" {
		b[c] = true
	}
	return b
}()
