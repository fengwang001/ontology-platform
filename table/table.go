// Package table 把 lexer 事件组装成表：列数一致、表头、行号定位。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Options 包含词法上限与记录数上限、首行表头选项。
type Options struct {
	MaxFieldBytes, MaxFields, MaxRecords int
	Header                               bool
}

// Table 是解析结果。Header 仅在 Options.Header 时填充；Rows 不含表头行。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Parser 是流式表组装器；单实例非并发安全。
type Parser struct {
	lx   *lexer.Lexer
	b    *builder
	opts Options
}

type builder struct {
	t       Table
	cur     []cell.Cell
	width   int
	records int
	opts    Options
}

func (b *builder) Field(c cell.Cell) { b.cur = append(b.cur, c) }

func (b *builder) EndRecord(end int) error {
	recNo := b.records + 1
	if b.opts.MaxRecords > 0 && recNo > b.opts.MaxRecords {
		return &lexer.PosError{Err: lexer.ErrTooManyRecords, Offset: end,
			Record: recNo, Field: len(b.cur) + 1}
	}
	if b.width == 0 {
		b.width = len(b.cur)
	} else if len(b.cur) != b.width {
		return &lexer.PosError{Err: lexer.ErrColumnCount, Offset: end,
			Record: recNo, Field: len(b.cur) + 1}
	}
	if b.opts.Header && b.records == 0 {
		b.t.Header = b.cur
	} else {
		b.t.Rows = append(b.t.Rows, b.cur)
	}
	b.records++
	b.cur = nil
	return nil
}

// New 创建流式解析器。
func New(opts Options) *Parser {
	b := &builder{opts: opts}
	lx := lexer.New(lexer.Options{MaxFieldBytes: opts.MaxFieldBytes,
		MaxFields: opts.MaxFields}, b)
	return &Parser{lx: lx, b: b, opts: opts}
}

func (p *Parser) Feed(d []byte) error { return p.lx.Feed(d) }
func (p *Parser) Close() error        { return p.lx.Close() }

// Table 返回已组装内容；错误后保留已产出的完整记录。
func (p *Parser) Table() Table { return p.b.t }

// Cells 为 writer 提供扁平记录视图（含表头时不含表头）。
func (t Table) Cells() [][]cell.Cell { return t.Rows }

// Parse 一次性解析整份输入。
func Parse(data []byte, opts Options) (Table, error) {
	p := New(opts)
	if e := p.Feed(data); e != nil {
		return p.b.t, e
	}
	if e := p.Close(); e != nil {
		return p.b.t, e
	}
	return p.b.t, nil
}
