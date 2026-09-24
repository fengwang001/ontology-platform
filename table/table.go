// Package table assembles lexer events into records with column consistency.
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Limits configure immediate rejection; zero means unlimited.
type Limits struct {
	MaxFieldBytes, MaxFieldsPerRecord, MaxRecords int
}

// Table is the parsed document; the first record is the header.
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

func (t *Table) Records() [][]cell.Cell {
	if t == nil {
		return nil
	}
	return append(append([][]cell.Cell{}, t.Header), t.Rows...)
}

// Builder consumes ordered lexer items; shared by streaming and par.
type Builder struct {
	lim         Limits
	t           Table
	cur         []cell.Cell
	ncol, recNo int
	active      bool
	term        *lexer.Error
}

// NewBuilder starts a fresh item builder.
func NewBuilder(lim Limits) *Builder { return &Builder{lim: lim} }

// Annotate fills record/field numbers on a lexer error from builder state.
func (b *Builder) Annotate(e *lexer.Error) *lexer.Error {
	if e.Record == 0 {
		e.Record = b.recNo
	}
	if e.Field == 0 {
		e.Field = len(b.cur) + 1
	}
	return e
}

// OnItem feeds one lexer item in order.
func (b *Builder) OnItem(it lexer.Item) *lexer.Error {
	if b.term != nil {
		return b.term
	}
	switch {
	case it.Err != nil:
		b.term = b.Annotate(it.Err)
	case it.Line >= 0:
		if b.active {
			if b.ncol > 0 && len(b.cur) != b.ncol {
				b.term = b.Annotate(&lexer.Error{Kind: lexer.ErrColumnMismatch, Offset: it.Line, Field: len(b.cur) + 1})
				return b.term
			}
			b.commit()
		}
		b.cur, b.active = nil, false
	default:
		c := it.Field
		if !b.active {
			b.active, b.recNo = true, b.recNo+1
			if b.lim.MaxRecords > 0 && b.recNo > b.lim.MaxRecords {
				b.term = &lexer.Error{Kind: lexer.ErrTooManyRecords, Offset: c.Start, Record: b.recNo, Field: 1}
				return b.term
			}
		}
		if b.lim.MaxFieldsPerRecord > 0 && len(b.cur) >= b.lim.MaxFieldsPerRecord {
			b.term = b.Annotate(&lexer.Error{Kind: lexer.ErrTooManyFields, Offset: c.Start})
			return b.term
		}
		b.cur = append(b.cur, c)
	}
	return b.term
}

func (b *Builder) commit() {
	if b.t.Header == nil && len(b.t.Rows) == 0 {
		b.t.Header = b.cur
	} else {
		b.t.Rows = append(b.t.Rows, b.cur)
	}
	b.ncol = len(b.cur)
}

// Flush commits a record ended by EOF rather than a newline.
func (b *Builder) Flush() *lexer.Error {
	if b.term != nil {
		return b.term
	}
	if b.active {
		if b.ncol > 0 && len(b.cur) != b.ncol {
			off := 0
			if len(b.cur) > 0 {
				off = b.cur[len(b.cur)-1].End
			}
			b.term = b.Annotate(&lexer.Error{Kind: lexer.ErrColumnMismatch, Offset: off, Field: len(b.cur) + 1})
			return b.term
		}
		b.commit()
		b.cur, b.active = nil, false
	}
	return nil
}

// Table returns the assembled table.
func (b *Builder) Table() *Table { return &b.t }

// Parser is a resumable streaming parser. Not safe for concurrent use.
type Parser struct {
	b  *Builder
	lx *lexer.L
}

// NewParser starts a streaming parser.
func NewParser(lim Limits) *Parser {
	b := NewBuilder(lim)
	return &Parser{b: b, lx: lexer.New(0, lim.MaxFieldBytes, func(it lexer.Item) { b.OnItem(it) })}
}

// Feed appends bytes; after the first error the parser is terminal.
func (p *Parser) Feed(data []byte) error {
	if e := p.lx.Feed(data); e != nil {
		if p.b.term != nil {
			return p.b.term
		}
		return p.b.Annotate(asLex(e))
	}
	return nil
}

// Close ends the stream and returns the assembled table.
func (p *Parser) Close() (*Table, error) {
	if e := p.lx.Close(); e != nil {
		if p.b.term != nil {
			return nil, p.b.term
		}
		return nil, p.b.Annotate(asLex(e))
	}
	if e := p.b.Flush(); e != nil {
		return nil, e
	}
	return p.b.Table(), nil
}

// Parse parses a complete buffer in one call.
func Parse(data []byte, lim Limits) (*Table, *lexer.Error) {
	p := NewParser(lim)
	if e := p.Feed(data); e != nil {
		return nil, asLex(e)
	}
	t, err := p.Close()
	if err != nil {
		return nil, asLex(err)
	}
	return t, nil
}

func asLex(err error) *lexer.Error {
	e, _ := err.(*lexer.Error)
	return e
}
