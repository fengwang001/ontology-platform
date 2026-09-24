// Package table 把 lexer 事件组装成记录与表，负责列数一致性、表头与上限。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// 列数与两条可在 sink 层「立刻」判定的上限错误。
var (
	ErrColumnMismatch = errors.New("record field count differs from first record")
	ErrTooManyFields  = errors.New("record exceeds MaxFieldsPerRecord")
	ErrTooManyRecords = errors.New("input exceeds MaxRecords")
)

// Limits 配置三类上限；<=0 表示不限制。
type Limits struct {
	MaxFieldBytes      int
	MaxFieldsPerRecord int
	MaxRecords         int
}

// Table 是组装结果。Header 为第一条记录；Records 含全部记录（含表头）。
type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
}

func tag(k error, off, rec, fld int) *lexer.Error {
	return &lexer.Error{Kind: k, Offset: off, Record: rec, Field: fld}
}

// Builder 是 lexer.Sink，可被 par 复用做手工拼接。非并发安全。
type Builder struct {
	lim     Limits
	cur     []cell.Cell
	nrec    int
	endOff  int
	Table   Table
	TermErr *lexer.Error
}

// NewBuilder 创建组装器。
func NewBuilder(lim Limits) *Builder { return &Builder{lim: lim} }

// NRecords 返回已闭合的记录数（含表头）。
func (b *Builder) NRecords() int { return b.nrec }

// EndOffset 返回最近一次产出的结束字节偏移。
func (b *Builder) EndOffset() int { return b.endOff }

// BeginRecord 为新记录准备字段缓冲（par 拼接用）。
func (b *Builder) BeginRecord() { b.cur = b.cur[:0] }

// Append 手工追加一个字段（par 拼接用），立即执行字段数上限。
func (b *Builder) Append(c cell.Cell) *lexer.Error {
	if b.lim.MaxFieldsPerRecord > 0 && len(b.cur) >= b.lim.MaxFieldsPerRecord {
		return tag(ErrTooManyFields, c.Start, b.nrec+1, len(b.cur)+1)
	}
	b.cur = append(b.cur, c)
	b.endOff = c.End
	return nil
}

// Commit 闭合当前记录，立即执行列数一致性与记录数上限。
func (b *Builder) Commit() *lexer.Error {
	if b.lim.MaxRecords > 0 && b.nrec >= b.lim.MaxRecords {
		return tag(ErrTooManyRecords, b.endOff, b.nrec+1, len(b.cur))
	}
	rec := append([]cell.Cell(nil), b.cur...)
	if b.nrec == 0 {
		b.Table.Header = rec
	} else if len(rec) != len(b.Table.Header) {
		return tag(ErrColumnMismatch, b.endOff, b.nrec+1, len(rec))
	}
	b.Table.Records = append(b.Table.Records, rec)
	b.nrec++
	b.cur = b.cur[:0]
	return nil
}

// OnField 实现 lexer.Sink。
func (b *Builder) OnField(c cell.Cell) error {
	if e := b.Append(c); e != nil {
		b.TermErr = e
		return e
	}
	return nil
}

// OnEndRecord 实现 lexer.Sink。
func (b *Builder) OnEndRecord() error {
	if e := b.Commit(); e != nil {
		b.TermErr = e
		return e
	}
	return nil
}

// Parser 是单线程流式解析器。单实例非并发安全。
type Parser struct {
	b   *Builder
	lex *lexer.Parser
}

// New 创建流式解析器。
func New(lim Limits) *Parser {
	b := NewBuilder(lim)
	return &Parser{b: b, lex: lexer.NewParser(b, lim.MaxFieldBytes)}
}

// BytesProcessed 返回词法状态机处理过的字节数（每字节恰好一次）。
func (p *Parser) BytesProcessed() int64 { return p.lex.BytesProcessed() }

// Feed 送入一段字节。
func (p *Parser) Feed(d []byte) error { return p.lex.Feed(d) }

// Close 结束流。
func (p *Parser) Close() error { return p.lex.Close() }

// Result 返回已组装的表（出错时保留已产出的完整记录前缀）。
func (p *Parser) Result() *Table { return &p.b.Table }

// Parse 一次性解析整个输入。
func Parse(data []byte, lim Limits) (*Table, error) {
	p := New(lim)
	if err := p.Feed(data); err != nil {
		return p.Result(), err
	}
	err := p.Close()
	return p.Result(), err
}
