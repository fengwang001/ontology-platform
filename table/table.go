// Package table 把 lexer 的事件组装成记录与表，负责上限、列数一致性与错误定位。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// 上限错误与列数错误，彼此及与 lexer 错误可用 errors.Is 区分。
var (
	ErrFieldTooLarge  = errors.New("table: field exceeds MaxFieldBytes")
	ErrTooManyFields  = errors.New("table: record exceeds MaxFields")
	ErrTooManyRecords = errors.New("table: table exceeds MaxRecords")
	ErrColumnCount    = errors.New("table: record column count mismatch")
)

// Limits 为三类上限；零值表示不限。
type Limits struct {
	MaxFieldBytes  int
	MaxFields      int
	MaxRecords     int
}

// Error 携带出错字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Table 是解析结果。Header 为 nil 表示无表头。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// RecCount 返回记录总数（不含表头）。
func (t *Table) RecCount() int { return len(t.Rows) }

// Builder 是 lexer.Sink 的记录组装器，流式 Parser 与并行拼接共用。
type Builder struct {
	lim     Limits
	records int
	fields  int
	cols    int

	header bool
	tbl    Table

	cur  []cell.Cell
	off  int
	q    bool
	val  []byte
	fs   int
	ferr error
}

// NewBuilder 构造组装器。header 为真时首条记录成为表头。
func NewBuilder(lim Limits, header bool) *Builder {
	return &Builder{lim: lim, header: header}
}

// Fail 用语义化当前坐标包装错误。
func (b *Builder) Fail(err error, off int) *Error {
	return b.fail(err, off, b.fields)
}

// FailAtField 用显式字段号包装语法错误（并行拼接做坐标换算时使用）。
func (b *Builder) FailAtField(err error, off, field int) *Error {
	return b.fail(err, off, field)
}

// Err 返回组装器已经进入的终态错误（上限或列数）。
func (b *Builder) Err() error { return b.ferr }

func (b *Builder) fail(err error, off int, field int) *Error {
	rec := b.records + 1
	if field <= 0 {
		field = b.fields
	}
	return &Error{Err: err, Offset: off, Record: rec, Field: field}
}

func (b *Builder) FieldStart(off int, quoted bool) {
	if b.ferr != nil {
		return
	}
	b.fields++
	if b.lim.MaxFields > 0 && b.fields > b.lim.MaxFields {
		b.ferr = b.fail(ErrTooManyFields, off, b.fields)
		return
	}
	b.off, b.q, b.fs, b.val = off, quoted, off, b.val[:0]
}

func (b *Builder) FieldData(p []byte) {
	if b.ferr != nil {
		return
	}
	if b.lim.MaxFieldBytes > 0 && len(b.val)+len(p) > b.lim.MaxFieldBytes {
		b.ferr = b.fail(ErrFieldTooLarge, b.off+len(b.val), b.fields)
		return
	}
	b.val = append(b.val, p...)
}

func (b *Builder) FieldEnd(off int) {
	if b.ferr != nil {
		return
	}
	v := make([]byte, len(b.val))
	copy(v, b.val)
	b.cur = append(b.cur, cell.Cell{Value: v, Quoted: b.q, Start: b.fs, End: off})
}

func (b *Builder) Record() {
	if b.ferr != nil {
		return
	}
	if b.cols == 0 {
		b.cols = b.fields
		if b.header {
			b.tbl.Header = b.cur
			b.cur = nil
			b.fields = 0
			return
		}
	} else if b.fields != b.cols {
		b.ferr = b.fail(ErrColumnCount, 0, 0)
		return
	}
	b.records++
	if b.lim.MaxRecords > 0 && b.records > b.lim.MaxRecords {
		b.ferr = b.fail(ErrTooManyRecords, 0, 0)
		return
	}
	b.tbl.Rows = append(b.tbl.Rows, b.cur)
	b.cur = nil
	b.fields = 0
}

// Parser 是流式 CSV 解析器。单个实例不是并发安全的。
type Parser struct {
	b   *builder
	m   *lexer.Machine
	err error
}

// New 返回带上限的解析器。
func New(lim Limits) *Parser {
	b := NewBuilder(lim, false)
	return &Parser{b: b, m: lexer.New(b)}
}

// NewWithHeader 把首条记录作为表头（不计入 MaxRecords 与列数检查的首条记录）。
func NewWithHeader(lim Limits) *Parser {
	p := New(lim)
	p.b.header = true
	return p
}

// Feed 喂入任意长度的字节块，可调用任意次。
func (p *Parser) Feed(data []byte) error { return p.wrap(p.m.Feed(data)) }

// Close 结束输入。
func (p *Parser) Close() error { return p.wrap(p.m.Close()) }

func (p *Parser) wrap(err error) error {
	if p.err != nil {
		return p.err
	}
	if p.b.ferr != nil {
		p.err = p.b.ferr
		return p.err
	}
	if err != nil {
		p.err = p.b.fail(err, p.m.ErrOffset(), p.b.fields)
	}
	return p.err
}

// Table 返回已成功组装的完整记录（出错时保留前缀）。
func (p *Parser) Table() *Table { return &p.b.tbl }

// Err 返回组装器当前错误（终态）。
func (p *Parser) Err() error { return p.err }

// Parse 一次性解析整个缓冲区。
func Parse(data []byte, lim Limits) (*Table, error) {
	p := New(lim)
	if err := p.Feed(data); err != nil {
		return p.Table(), err
	}
	err := p.Close()
	return p.Table(), err
}
