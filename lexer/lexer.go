package lexer

import "errors"

var ErrBareQuote = errors.New("quote in unquoted field")
var ErrQuoteClosed = errors.New("character after closed quote")
var ErrUnterminatedQuote = errors.New("unterminated quoted field")
var ErrBareCR = errors.New("bare carriage return")

type Limits struct {
	MaxFieldBytes int
	MaxFields      int
	MaxRecords     int
}

type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Event struct {
	Kind  int
	Start int
	End   int
	Text  string
}

type Lexer struct{}

func New(emit func(Event), limits Limits) *Lexer { return &Lexer{} }
func (l *Lexer) Feed(p []byte) error            { return nil }
func (l *Lexer) Close() error                   { return nil }
func (l *Lexer) BytesProcessed() int64          { return 0 }
