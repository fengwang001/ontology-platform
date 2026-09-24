// Package table 把 lexer 事件组装成表：列数一致性、表头、行列定位、上限判定。
// 单个 Parser/Builder 实例不是并发安全的。
package table

import (
	"errors"
	"fmt"
	"ontology/cell"
	"ontology/lexer"
)

var (
	ErrColumnCount    = errors.New("csv: inconsistent column count")
	ErrTooManyRecords = errors.New("csv: too many records")
)

// Error 表级错误：Record/Field 从 1 起，Offset 从 0 起。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d, field %d)", e.Err, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

type Limits struct {
	MaxFieldBytes int // 单字段最大内容字节数，0 不限
	MaxFields     int // 单记录最大字段数，0 不限
	MaxRecords    int // 最大记录数，0 不限
}

// Table 第一条记录是表头，其余是数据行。
type Table struct {
	Header []cell.Field
	Rows   [][]cell.Field
}

// Cell 按行列定位：row 0 为表头，row>=1 为数据行，col 从 0 起。
func (t Table) Cell(row, col int) (cell.Field, bool) {
	rec := t.Header
	if row > 0 {
		if row > len(t.Rows) {
			return cell.Field{}, false
		}
		rec = t.Rows[row-1]
	}
	if col < 0 || col >= len(rec) {
		return cell.Field{}, false
	}
	return rec[col], true
}

// Builder 从事件流组装 Table，并按全局计数即时判定三类上限。
type Builder struct {
	lim    Limits
	tab    Table
	cur    []cell.Field
	fld    cell.Field
	buf    []byte
	nBytes int
	nRec   int
	last   int
	open   bool
	err    error
}

func NewBuilder(lim Limits) *Builder { return &Builder{lim: lim} }

func (b *Builder) Add(ev lexer.Event) {
	if b.err != nil {
		return
	}
	switch ev.Op {
	case lexer.OpFieldStart:
		b.fld = cell.Field{Quoted: ev.Quoted, Start: ev.Offset}
		b.buf, b.nBytes, b.open = b.buf[:0], 0, true
	case lexer.OpData:
		b.nBytes++
		if b.lim.MaxFieldBytes > 0 && b.nBytes > b.lim.MaxFieldBytes {
			b.fail(lexer.ErrTooManyFieldBytes, ev.Offset)
			return
		}
		b.buf = append(b.buf, ev.Data)
	case lexer.OpFieldEnd:
		if b.lim.MaxFields > 0 && len(b.cur)+1 > b.lim.MaxFields {
			b.fail(lexer.ErrTooManyFields, ev.Offset)
			return
		}
		b.fld.Value, b.fld.End = string(b.buf), ev.Offset
		b.cur = append(b.cur, b.fld)
		b.last, b.open = ev.Offset, false
	case lexer.OpRecordEnd:
		b.finishRecord()
	}
}

func (b *Builder) Err() error      { return b.err }
func (b *Builder) Table() Table    { return b.tab }
func (b *Builder) NRec() int       { return b.nRec }
func (b *Builder) NFields() int    { return len(b.cur) }
func (b *Builder) FieldOpen() bool { return b.open }

func (b *Builder) fail(err error, off int) {
	b.err = &Error{Err: err, Offset: off, Record: b.nRec + 1, Field: len(b.cur) + 1}
}

func (b *Builder) finishRecord() {
	n := b.nRec + 1
	if b.lim.MaxRecords > 0 && n > b.lim.MaxRecords {
		b.err = &Error{Err: ErrTooManyRecords, Offset: b.last, Record: n, Field: len(b.cur)}
		return
	}
	if b.tab.Header == nil {
		b.tab.Header = b.cur
	} else if len(b.cur) != len(b.tab.Header) {
		b.err = &Error{Err: ErrColumnCount, Offset: b.last, Record: n, Field: len(b.cur)}
		return
	} else {
		b.tab.Rows = append(b.tab.Rows, b.cur)
	}
	b.cur, b.nRec = nil, b.nRec+1
}

// Parser 流式解析器：Feed 可任意多次调用，Close 结束。
type Parser struct {
	lex *lexer.Lexer
	b   *Builder
	err error
}

func NewParser(lim Limits) *Parser {
	return &Parser{lex: lexer.New(lim.MaxFieldBytes, lim.MaxFields), b: NewBuilder(lim)}
}

func (p *Parser) Feed(buf []byte) error {
	if p.err != nil {
		return p.err
	}
	lerr := p.lex.Feed(buf, p.b.Add)
	return p.collect(lerr)
}

func (p *Parser) Close() error {
	if p.err != nil {
		return p.err
	}
	return p.collect(p.lex.Close(p.b.Add))
}

func (p *Parser) collect(lerr error) error {
	if p.b.Err() != nil {
		p.err = p.b.Err()
	} else if lerr != nil {
		p.err = lerr
	}
	return p.err
}

func (p *Parser) Table() Table { return p.b.Table() }

func Parse(buf []byte, lim Limits) (Table, error) {
	p := NewParser(lim)
	if err := p.Feed(buf); err != nil {
		return p.Table(), err
	}
	return p.Table(), p.Close()
}
