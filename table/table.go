package table

import (
	"ontology/cell"
	"ontology/lexer"
)

type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
}

type Parser struct {
	lex     *lexer.Machine
	tab     Table
	current []cell.Cell
	width   int
	lim     lexer.Limits
	err     error
}

func NewParser(lim lexer.Limits) *Parser {
	p := &Parser{lim: lim}
	p.lex = lexer.New(p, lim)
	return p
}

func Parse(input []byte, lim lexer.Limits) (Table, error) {
	p := NewParser(lim)
	if err := p.Feed(input); err != nil {
		return p.Table(), err
	}
	if err := p.Close(); err != nil {
		return p.Table(), err
	}
	return p.Table(), nil
}

func (p *Parser) Feed(data []byte) error {
	if p.err != nil {
		return p.err
	}
	if err := p.lex.Feed(data); err != nil {
		p.err = err
	}
	return p.err
}

func (p *Parser) Close() error {
	if p.err != nil {
		return p.err
	}
	if err := p.lex.Close(); err != nil {
		p.err = err
	}
	return p.err
}

func (p *Parser) Field(c cell.Cell) error {
	if p.lim.MaxFields > 0 && c.Field > p.lim.MaxFields {
		return &lexer.Error{Kind: lexer.KindFieldsLimit, Offset: c.Start, Record: c.Record, Field: c.Field}
	}
	if p.width > 0 && c.Field > p.width {
		return &lexer.Error{Kind: lexer.KindColumnCount, Offset: c.Start, Record: c.Record, Field: c.Field}
	}
	p.current = append(p.current, c)
	return nil
}

func (p *Parser) RecordEnd(offset int64) error {
	cells := p.current
	p.current = nil
	if p.width == 0 {
		p.width = len(cells)
		p.tab.Header = cells
		return nil
	}
	if len(cells) != p.width {
		rec, field := 1, len(cells)+1
		if len(cells) > 0 {
			rec = cells[0].Record
		}
		return &lexer.Error{Kind: lexer.KindColumnCount, Offset: offset, Record: rec, Field: field}
	}
	if p.lim.MaxRecords > 0 && len(p.tab.Header)+len(p.tab.Records) >= p.lim.MaxRecords {
		return &lexer.Error{Kind: lexer.KindRecordsLimit, Offset: offset, Record: cells[0].Record, Field: 1}
	}
	p.tab.Records = append(p.tab.Records, cells)
	return nil
}

func (p *Parser) Table() Table {
	out := Table{Header: append([]cell.Cell(nil), p.tab.Header...), Records: make([][]cell.Cell, len(p.tab.Records))}
	for i, row := range p.tab.Records {
		out.Records[i] = append([]cell.Cell(nil), row...)
	}
	return out
}

func (p *Parser) EmitEnd(offset int64) error {
	return p.RecordEnd(offset)
}

func (p *Parser) Bytes() int64 { return p.lex.Bytes() }
