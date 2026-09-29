package lexer

import "ontology/cell"

type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

type Sink interface {
	Field(cell.Cell)
	Record()
}

type Lexer struct{}

func New(Sink, Limits) *Lexer { return &Lexer{} }

func (l *Lexer) Feed([]byte) error  { return nil }
func (l *Lexer) Close() error       { return nil }
func (l *Lexer) Processed() uint64  { return 0 }
