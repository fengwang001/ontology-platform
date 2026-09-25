// Package table 把 lexer 事件组装成表并做列数/上限检查。
package table

import (
	"strings"

	"ontology/cell"
	"ontology/lexer"
)

// Table 为解析结果。
type Table struct{ Records [][]cell.Cell }

// Option 配置解析器。
type Option func(*Parser)

// WithLimits 配置三类上限。
func WithLimits(m lexer.Limits) Option { return func(p *Parser) { p.lim = m } }

// Parser 为流式解析器（单实例非并发安全）。
type Parser struct {
	lim lexer.Limits
	lex *lexer.Lexer
	b   *Builder
	err error
}

// New 创建流式解析器。
func New(opts ...Option) *Parser {
	p := &Parser{}
	for _, o := range opts {
		o(p)
	}
	p.b = NewBuilder(p.lim)
	p.lex = lexer.New(p.b, 0, lexer.Clear, 0, 0, false, p.lim)
	return p
}

// Feed 喂入字节；出错后进入终态，重复 Feed/Close 返回同一错误。
func (p *Parser) Feed(b []byte) error {
	if p.err != nil {
		return p.err
	}
	p.err = p.lex.Feed(b)
	return p.err
}

// Close 结束流并返回表；已产出的完整记录保留在 Builder 中。
func (p *Parser) Close() (*Table, error) {
	if p.err == nil {
		p.err = p.lex.Close(true)
	}
	if p.err != nil {
		return nil, p.err
	}
	return &Table{Records: p.b.Records()}, nil
}

// Parse 一次性解析。
func Parse(b []byte, opts ...Option) (*Table, error) {
	p := New(opts...)
	if err := p.Feed(b); err != nil {
		return nil, err
	}
	return p.Close()
}

// BytesProcessed 返回状态机处理字节计数。
func (p *Parser) BytesProcessed() int { return p.lex.N() }

// Builder 是流式与 par 共用的 Sink：列数一致性、字段/记录上限。
type Builder struct {
	lim                                          lexer.Limits
	recs                                         [][]cell.Cell
	row                                          []cell.Cell
	sb                                           strings.Builder
	start, end                                   int
	quoted                                       bool
	err                                          *lexer.Error
}

// NewBuilder 创建组装器（字段长度上限由 lexer 强制）。
func NewBuilder(m lexer.Limits) *Builder { return &Builder{lim: m} }

// BeginField 开始字段。
func (b *Builder) BeginField(start int, quoted bool) error {
	b.start, b.end, b.quoted = start, start, quoted
	return nil
}

// Append 追加解码字节。
func (b *Builder) Append(p []byte) error { b.sb.Write(p); return nil }

// SetEnd 更新跨切点未定稿字段的终点（par 用）。
func (b *Builder) SetEnd(end int) { b.end = end }

// EndField 定稿字段，立即执行字段数/列数检查。
func (b *Builder) EndField(end int) (cell.Cell, error) {
	c := cell.Cell{Value: b.sb.String(), Quoted: b.quoted, Start: b.start, End: end}
	b.sb.Reset()
	b.end = end
	n := len(b.row) + 1
	if b.err == nil {
	switch {
	case b.lim.MaxFields > 0 && n > b.lim.MaxFields:
		b.err = &lexer.Error{Kind: lexer.TooManyFields, Byte: end, Record: b.recno(), Field: n}
	case len(b.recs) > 0 && n > len(b.recs[0]):
		b.err = &lexer.Error{Kind: lexer.ColumnMismatch, Byte: end, Record: b.recno(), Field: n}
	}
	b.row = append(b.row, c)
	return c, b.Err()
}

// EndRecord 落定记录，立即执行列数/记录数检查；超限不落定。
func (b *Builder) EndRecord() error {
	n := len(b.row)
	switch {
	case b.err != nil:
	case len(b.recs) > 0 && n != len(b.recs[0]):
		b.err = &lexer.Error{Kind: lexer.ColumnMismatch, Byte: b.end, Record: b.recno(), Field: n}
	case b.lim.MaxRecords > 0 && len(b.recs) >= b.lim.MaxRecords:
		b.err = &lexer.Error{Kind: lexer.TooManyRecords, Byte: b.end, Record: b.recno(), Field: n}
	default:
		b.recs = append(b.recs, b.row)
	}
	b.row = nil
	return b.Err()
}

func (b *Builder) recno() int { return len(b.recs) + 1 }

// Records 返回已落定的完整记录。
func (b *Builder) Records() [][]cell.Cell { return b.recs }

// Err 返回终态错误。
func (b *Builder) Err() error {
	if b.err == nil {
		return nil
	}
	return b.err
}
