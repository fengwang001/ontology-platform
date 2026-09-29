package table

import (
	"errors"
	"fmt"
	"ontology/cell"
	"ontology/lexer"
)

type Limits = lexer.Limits

type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
}

type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

var ErrColumnMismatch = errors.New("record field count differs from first record")

type Builder struct {
	t Table
	row []cell.Cell
	bytes []byte
	start, record, field int
	open bool
	err error
}

func NewBuilder() *Builder { return &Builder{record: 1} }

func (b *Builder) Start(off int) error {
	b.open = true
	b.field++
	b.start = off
	b.bytes = b.bytes[:0]
	return nil
}

func (b *Builder) Append(data []byte) error {
	b.bytes = append(b.bytes, data...)
	return nil
}

func (b *Builder) EndField(off int, quoted bool) {
	if !b.open { return }
	b.row = append(b.row, cell.Cell{Value: string(b.bytes), Quoted: quoted, Start: b.start, End: off})
	b.open = false
}

func (b *Builder) EndRecord(off int) {
	if len(b.t.Header) == 0 {
		b.t.Header = b.row
	} else if len(b.row) != len(b.t.Header) && b.err == nil {
		b.err = &PosError{Err: ErrColumnMismatch, Offset: off, Record: b.record, Field: len(b.row)+1}
	}
	if b.err == nil {
		if b.record > 1 { b.t.Records = append(b.t.Records, b.row) }
		b.record++
	}
	b.row, b.field = nil, 0
}

func (b *Builder) Finish() *Table {
	if b.open {
		b.EndField(0, false)
	}
	if len(b.t.Header) == 0 {
		b.t.Header = b.row
	} else if len(b.row) > 0 && len(b.row) == len(b.t.Header) {
		if b.record > 1 { b.t.Records = append(b.t.Records, b.row) }
	}
	return &b.t
}

func (b *Builder) Error(err error) error {
	if err == nil { return b.err }
	var pe *lexer.PosError
	if errors.As(err, &pe) {
		return &PosError{Err: pe.Err, Offset: pe.Offset, Record: b.record, Field: max(b.field, 1)}
	}
	return err
}

type Parser struct {
	lex *lexer.Machine
	b *Builder
	err error
}

func New(lim Limits) *Parser { return &Parser{lex: lexer.New(lim), b: NewBuilder()} }

func (p *Parser) Feed(data []byte) error {
	if p.err != nil {
		if errors.Is(p.err, lexer.ErrTerminal) { return p.err }
		p.err = &PosError{Err: lexer.ErrTerminal, Offset: -1, Record: p.b.record, Field: max(p.b.field, 1)}
		return p.err
	}
	err := p.lex.Feed(data, p.b)
	if err != nil || p.b.err != nil {
		p.err = p.b.Error(err)
		return p.err
	}
	return nil
}

func (p *Parser) Close() (*Table, error) {
	if p.err != nil { return nil, p.Feed(nil) }
	err := p.lex.Close(p.b)
	if err != nil || p.b.err != nil {
		p.err = p.b.Error(err)
		return nil, p.err
	}
	return p.b.Finish(), nil
}

func (p *Parser) BytesProcessed() int64 { return p.lex.BytesProcessed() }
