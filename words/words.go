// Package words splits shell command lines without expansions or redirection.
package words

import (
	"errors"
	"strings"

	"ontology/lex"
)

var (
	// ErrUnterminatedSingle matches an unmatched opening single quote.
	ErrUnterminatedSingle = lex.ErrUnterminatedSingle
	// ErrUnterminatedDouble matches an unmatched opening double quote.
	ErrUnterminatedDouble = lex.ErrUnterminatedDouble
	// ErrDanglingBackslash matches a final lone backslash.
	ErrDanglingBackslash = lex.ErrDanglingBackslash
)

// SyntaxError is the offset-bearing parsing failure returned by Split.
type SyntaxError = lex.SyntaxError

// Split performs shell-style quoting and whitespace tokenization.
func Split(input string) ([]string, error) {
	splitter := NewSplitter()
	if err := splitter.Feed(input); err != nil {
		return nil, err
	}
	if err := splitter.Close(); err != nil {
		return nil, err
	}
	return splitter.Words(), nil
}

// Splitter accepts arbitrary chunks while preserving lexical boundaries.
type Splitter struct {
	machine lex.Machine
	current strings.Builder
	words   []string
	closed  bool
}

// NewSplitter creates an empty streaming splitter.
func NewSplitter() *Splitter {
	return &Splitter{}
}

// Feed appends a chunk and flushes any words whose delimiters are known.
func (s *Splitter) Feed(chunk string) error {
	if s.closed {
		return errors.New("words: feed after close")
	}
	for index := 0; index < len(chunk); index++ {
		event := s.machine.Step(chunk[index], s.machine.Processed())
		switch event.Kind {
		case lex.Bytes:
			s.current.Write(event.Payload)
		case lex.End:
			s.finishWord()
		}
	}
	return nil
}

// Close validates end-of-input and emits any pending word.
func (s *Splitter) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.machine.Close(); err != nil {
		return err
	}
	if s.machine.InWord() {
		s.finishWord()
	}
	return nil
}

// Words returns a copy of all completed words.
func (s *Splitter) Words() []string {
	if s.closed {
		return append([]string(nil), s.words...)
	}
	if s.machine.InWord() {
		result := append([]string(nil), s.words...)
		return append(result, s.current.String())
	}
	return append([]string(nil), s.words...)
}

// Quote renders words so Split can recover them exactly.
func Quote(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quoteWord(value)
	}
	return strings.Join(quoted, " ")
}

func (s *Splitter) finishWord() {
	s.words = append(s.words, s.current.String())
	s.current.Reset()
}

func quoteWord(value string) string {
	if value == "" {
		return "''"
	}

	if needsQuote(value) {
		return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
	}
	return value
}

func needsQuote(value string) bool {
	if value == "" {
		return true
	}
	for index := 0; index < len(value); index++ {
		b := value[index]
		if b == ' ' || b == '\t' || b == '\n' || b == '\'' || b == '"' || b == '\\' || b == '$' || b == '`' || b == ';' || b == '|' || b == '&' || b == '#' || b == '<' || b == '>' || b == '(' || b == ')' || b == '*' || b == '?' || b == '[' || b == ']' {
			return true
		}
	}
	return false
}
