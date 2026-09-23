// Package words splits a command line into POSIX-shell-style words,
// handling quoting and backslashes only (no expansion, globs, command
// substitution or redirection). It also re-quotes words into a line that
// Split can reconstruct exactly.
package words

import "ontology/lex"

// Re-exported lexical errors so callers need not import lex.
var (
	ErrUnterminatedSingleQuote = lex.ErrUnterminatedSingleQuote
	ErrUnterminatedDoubleQuote = lex.ErrUnterminatedDoubleQuote
	ErrTrailingBackslash       = lex.ErrTrailingBackslash
)

// LexError exposes the failing byte offset; it wraps one of the three
// sentinel errors above.
type LexError = lex.Error

// Splitter is a streaming tokenizer: feed arbitrary byte fragments in any
// chunking, then Close. Words snapshots the words recognized so far.
type Splitter struct {
	lx     lex.Lexer
	base   int    // absolute offset of the current fragment start
	cur    []byte // word being built
	inWord bool
	done   []string
	err    error
}

func (s *Splitter) flush() {
	s.done = append(s.done, string(s.cur))
	s.cur = s.cur[:0]
	s.inWord = false
}

// Feed consumes one fragment. Any chunking of the same full input yields
// identical results.
func (s *Splitter) Feed(chunk string) {
	for i := 0; i < len(chunk); i++ {
		s.lx.Step(chunk[i], s.base+i)
	}
	s.base += len(chunk)
	for _, it := range s.lx.Items() {
		switch it.Kind {
		case lex.Begin:
			if s.inWord && len(s.cur) == 0 {
				s.done = append(s.done, "")
				s.inWord = false
			}
			s.inWord = true
		case lex.Lit:
			s.inWord = true
			s.cur = append(s.cur, it.Data)
		case lex.Sep:
			if s.inWord {
				s.flush()
			}
		}
	}
}

// Close marks the end of input and finalizes the last word; it returns the
// terminal lexical error, if any.
func (s *Splitter) Close() error {
	if s.err != nil {
		return s.err
}
	s.err = s.lx.Close()
	if s.err == nil && s.inWord {
		s.flush()
}
	return s.err
}

// Words returns the finalized words plus the in-progress word. The result
// after feeding any prefix up to and including the complete input (then
// Close) is the full word list.
func (s *Splitter) Words() []string {
	out := make([]string, len(s.done))
	copy(out, s.done)
	if s.inWord {
		out = append(out, string(s.cur))
	}
	return out
}

// Split tokenizes a complete line.
func Split(s string) ([]string, error) {
	var sp Splitter
	sp.Feed(s)
	if err := sp.Close(); err != nil {
		return nil, err
	}
	return sp.Words(), nil
}

// Quote renders words as one line that Split reconstructs exactly. Every
// word containing a shell-significant byte is wrapped in single quotes,
// which preserve everything literally; a single quote inside a word cannot
// be escaped inside single quotes (rule 1), so it is emitted as the
// standard '\'' sequence: end quote, escaped quote, reopen quote.
func Quote(words []string) string {
	out := make([]byte, 0, len(words)*4)
	for i, w := range words {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, quoteWord(w)...)
	}
	return string(out)
}

func quoteWord(w string) []byte {
	if w == "" {
		return []byte("''")
	}
	safe := true
	for i := 0; i < len(w); i++ {
		c := w[i]
		if c <= ' ' || c == '\'' || c == '"' || c == '\\' || c == '$' ||
			c == '`' || c == ';' || c == '&' || c == '|' || c == '<' ||
			c == '>' || c == '(' || c == ')' || c == '*' || c == '?' ||
			c == '[' || c == ']' || c == '#' || c == '~' || c == '=' ||
			c == '{' || c == '}' {
			safe = false
			break
		}
	}
	if safe {
		return []byte(w)
	}
	var b []byte
	b = append(b, '\'')
	for i := 0; i < len(w); i++ {
		if w[i] == '\'' {
			b = append(b, '\'', '\\', '\'', '\'')
		} else {
			b = append(b, w[i])
		}
	}
	b = append(b, '\'')
	return b
}
