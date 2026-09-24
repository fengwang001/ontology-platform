// Package table 把词法事件组装成带表头与列数校验的表。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnCount 是记录列数与首条记录不一致的哨兵错误。
var ErrColumnCount = errors.New("record field count does not match header")

// Table 是成功解析的结果。Header 为第一条记录，Rows 为其余记录。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Records 返回含表头在内的全部记录。
func (t *Table) Records() [][]cell.Cell {
	out := make([][]cell.Cell, 0, 1+len(t.Rows))
	out = append(out, t.Header)
	return append(out, t.Rows...)
}

type builder struct {
	t      *Table
	cur    []cell.Cell
	width  int
	closed bool
}

func (b *builder) OnField(c cell.Cell) { b.cur = append(b.cur, c) }

func (b *builder) OnRecord() error {
	rec := b.cur
	b.cur = nil
	if !b.closed {
		b.t.Header, b.width, b.closed = rec, len(rec), true
		return nil
	}
	if len(rec) != b.width {
		// 记录号：表头为 1，故当前为 len(Rows)+2；位置指向多出/缺少字段处。
		return &lexer.ParseError{Err: ErrColumnCount, Off: rec[0].Start,
			Record: len(b.t.Rows) + 2, Field: len(rec) + 1}
	}
	b.t.Rows = append(b.t.Rows, rec)
	return nil
}

// Parser 是流式表构建器。非并发安全。
type Parser struct {
	lx *lexer.Lexer
	b  *builder
}

// New 创建解析器。
func New(lim lexer.Limits) *Parser {
	b := &builder{t: &Table{}}
	return &Parser{lx: lexer.New(b, lim), b: b}
}

// Feed 送入一段字节。
func (p *Parser) Feed(d []byte) error { return p.lx.Feed(d) }

// Close 收尾，成功时返回 Table。
func (p *Parser) Close() (*Table, error) {
	if e := p.lx.Close(); e != nil {
		return nil, e
	}
	return p.b.t, nil
}

// Bytes 返回底层词法器处理过的字节数。
func (p *Parser) Bytes() int { return p.lx.Bytes() }

// Parse 一次性解析。
func Parse(d []byte, lim lexer.Limits) (*Table, error) {
	p := New(lim)
	if e := p.Feed(d); e != nil {
		return nil, e
	}
	return p.Close()
}
