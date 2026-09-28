package table

import (
	"ontology/cell"
	"ontology/lexer"
)

type Row struct {
	Cells []cell.Cell
	Line  int // 记录号（1 起）
}

type Table struct {
	Rows      []Row
	Header    []cell.Cell
	HasHeader bool
}

type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int // 0 不限
	WithHeader    bool
}

type ParseError struct {
	Kind   lexer.Kind
	Offset int
	Record int
	Field  int
}

func (e *ParseError) Error() string { return "csv parse error" }

type Parser struct {
	lex   *lexer.Lexer
	lim   Limits
	tab   *Table
	cur   []cell.Cell
	cellN int
	fatal *ParseError
}

func NewParser(lim Limits) *Parser {
	p := &Parser{lim: lim, tab: &Table{}}
	p.lex = lexer.New(p, lexer.Limits{MaxFieldBytes: lim.MaxFieldBytes, MaxFields: lim.MaxFields})
	return p
}

func (p *Parser) Field(c cell.Cell) {
	p.cellN++
	p.cur = append(p.cur, c)
}

func (p *Parser) EndRecord() {
	if p.lim.WithHeader && !p.tab.HasHeader {
		p.tab.Header = p.cur
		p.tab.HasHeader = true
		p.cur = nil
		return
	}
	n := len(p.tab.Rows) + 1
	if p.lim.MaxRecords > 0 && n > p.lim.MaxRecords {
		p.fail(lexer.ErrTooManyRecords, p.cur[len(p.cur)-1].Start)
		return
	}
	if len(p.tab.Rows) > 0 && len(p.cur) != len(p.tab.Rows[0].Cells) {
		p.fail(lexer.ErrFieldCount, p.cur[0].Start)
		return
	}
	p.tab.Rows = append(p.tab.Rows, Row{Cells: p.cur, Line: p.lex.Records() + 1})
	p.cur = nil
}

func (p *Parser) Error(e *lexer.Error) {
	p.fatal = &ParseError{Kind: e.Kind, Offset: e.Offset, Record: e.Record, Field: e.Field}
}

func (p *Parser) fail(k lexer.Kind, off int) {
	p.fatal = &ParseError{Kind: k, Offset: off, Record: p.lex.Records() + 1, Field: 1}
}

func (p *Parser) Feed(b []byte) error {
	if p.fatal != nil {
		return p.fatal
	}
	if err := p.lex.Feed(b); err != nil {
		return p.fatal
	}
	return nil
}

func (p *Parser) Close() error {
	if p.fatal != nil {
		return p.fatal
	}
	if err := p.lex.Close(); err != nil {
		if p.fatal == nil {
			p.Error(err.(*lexer.Error))
		}
	}
	return p.fatal
}

func (p *Parser) Table() *Table { return p.tab }

// Parse 一次性解析（非流式调用方可直接用）。
func Parse(b []byte, lim Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(b); err != nil {
		return p.tab, err
	}
	if err := p.Close(); err != nil {
		return p.tab, err
	}
	return p.tab, nil
}
