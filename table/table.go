package table

import (
	"errors"
	"ontology/cell"
	"ontology/lexer"
)

var ErrColumnCount = errors.New("record column count mismatch")

// Table contains successfully assembled records.
type Table struct {
	Header  []cell.Cell
	Rows    [][]cell.Cell
	Records [][]cell.Cell
}

// Parser assures record arity while streaming cells.
type Parser struct {
	lex    *lexer.Lexer
	tab    *Table
	cur    []cell.Cell
	width  int
	err    error
}

func NewParser(lim lexer.Limits) *Parser {
	p := &Parser{tab: &Table{}}
	p.lex = lexer.New(lim,
		func(c cell.Cell) { p.cur = append(p.cur, c) },
		p.endRecord)
	return p
}

func (p *Parser) Feed(b []byte) error {
	if p.err != nil {
		return p.err
	}
	p.err = p.lex.Feed(b)
	return p.err
}

func (p *Parser) Close() error {
	if p.err != nil {
		return p.err
	}
	p.err = p.lex.Close()
	return p.err
}

func (p *Parser) Table() *Table { return p.tab }

func Parse(b []byte, lim lexer.Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(b); err != nil {
		return p.tab, err
	}
	return p.tab, p.Close()
}

func (p *Parser) endRecord(end int) error {
	if p.width == 0 {
		p.width = len(p.cur)
	} else if len(p.cur) != p.width {
		return ErrColumnCount
	}
	rec := append([]cell.Cell(nil), p.cur...)
	p.tab.Records = append(p.tab.Records, rec)
	if len(p.tab.Records) == 1 {
		p.tab.Header = rec
	} else {
		p.tab.Rows = append(p.tab.Rows, rec)
	}
	p.cur = p.cur[:0]
	return nil
}
