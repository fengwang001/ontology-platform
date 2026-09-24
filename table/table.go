// Package table 把词法器事件组装为带行列定位的表。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnMismatch 是记录列数与第一条记录不一致的错误。
var ErrColumnMismatch = errors.New("record column count mismatch")

// Table 是解析结果；Header 为空表示无表头数据。
type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
}

// Width 返回列数。
func (t *Table) Width() int {
	if len(t.Header) > 0 {
		return len(t.Header)
	}
	if len(t.Records) > 0 {
		return len(t.Records[0])
	}
	return 0
}

// Parse 一次性解析输入。lim 为上限（0 表示不限）。
func Parse(p []byte, lim lexer.Limits) (*Table, error) {
	b := NewBuilder(lim, false)
	if err := b.Parse(p); err != nil {
		return b.Table(), err
	}
	if err := b.Close(); err != nil {
		return b.Table(), err
	}
	return b.Table(), nil
}

// Builder 是流式组装器，实现 lexer.Sink；单实例非并发安全。
type Builder struct {
	lex  *lexer.Lexer
	lim  lexer.Limits
	hd   bool
	tab  Table
	cur  []cell.Cell
	width int
}

// NewBuilder 创建流式组装器；hasHeader 为真时第一条记录作为表头。
func NewBuilder(lim lexer.Limits, hasHeader bool) *Builder {
	b := &Builder{lim: lim, hd: hasHeader}
	b.lex = lexer.New(b, lim)
	return b
}

// Parse 等同于一次 Feed。
func (b *Builder) Parse(p []byte) error { return b.lex.Feed(p) }

// Feed 喂入字节。
func (b *Builder) Feed(p []byte) error { return b.lex.Feed(p) }

// Close 结束输入。
func (b *Builder) Close() error {
	if err := b.lex.Close(); err != nil {
		return err
	}
	return nil
}

// Table 返回目前已组装的表（出错时保留已产出的完整记录）。
func (b *Builder) Table() *Table {
	t := b.tab
	return &t
}

// Cell 实现 lexer.Sink。
func (b *Builder) Cell(c cell.Cell) error {
	b.cur = append(b.cur, c)
	return nil
}

// EndRecord 实现 lexer.Sink。
func (b *Builder) EndRecord(termOff int64) error {
	n := len(b.cur)
	if b.width == 0 {
		b.width = n
	} else if n != b.width {
		return &lexer.Error{Kind: ErrColumnMismatch, Offset: termOff,
			Record: len(b.tab.Records) + 1, Field: n + 1}
	}
	if b.hd && len(b.tab.Header) == 0 {
		b.tab.Header = b.cur
	} else {
		b.tab.Records = append(b.tab.Records, b.cur)
	}
	b.cur = nil
	return nil
}
