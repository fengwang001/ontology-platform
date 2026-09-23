// Package table 把 lexer 事件组装成记录，并校验列数一致性与上限。
package table

import (
	"errors"
	"strings"

	"ontology/cell"
	"ontology/lexer"
)

// 记录列数 / 记录数错误；字段级错误复用 lexer 哨兵。
var (
	ErrFieldCount     = errors.New("record field count differs from first record")
	ErrTooManyRecords = errors.New("record count exceeds MaxRecords")
)

// Error 是带全局坐标的解析错误。字节偏移从 0 起，记录号、字段号从 1 起。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// IsError 判断 err 是否属于指定的某一类错误（哨兵）。
func IsError(err, target error) bool { return errors.Is(err, target) }

// AsError 提取带坐标的错误；非本类型返回 nil。
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// Table 是解析出的表。Header 为第一条记录，Rows 为其余记录（含表头时 Header 非空）。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Records 返回全部记录（表头在前）。
func (t *Table) Records() [][]cell.Cell {
	if len(t.Header) == 0 {
		return t.Rows
	}
	return append(append([][]cell.Cell{}, t.Header), t.Rows...)
}

// Equal 逐字段比较值、引号标记与偏移。
func (t *Table) Equal(o *Table) bool {
	a, b := t.Records(), o.Records()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			x, y := a[i][j], b[i][j]
			if !x.Equal(y) || x.Start != y.Start || x.End != y.End {
				return false
			}
		}
	}
	return true
}

// Builder 实现 lexer.Sink。同一 Builder 不并发安全。
type Builder struct {
	lim       lexer.Limits
	buf       strings.Builder
	row       []cell.Cell
	curStart  int
	width     int
	recNo     int
	complete  [][]cell.Cell
	completed int
	finalErr  error
}

// NewBuilder 创建组装器。
func NewBuilder(lim lexer.Limits) *Builder { return &Builder{lim: lim} }

// Table 返回已组装的表；completed==0 时返回空表。
func (b *Builder) Table() *Table {
	t := &Table{}
	if b.completed > 0 {
		t.Header = b.complete[0]
	}
	if b.completed > 1 {
		t.Rows = append(t.Rows, b.complete[1:]...)
	}
	return t
}

// Err 返回组装过程中的首个错误。
func (b *Builder) Err() error { return b.finalErr }

// Completed 为已完整产出（含错误前缀）的记录数。
func (b *Builder) Completed() int { return b.completed }

func (b *Builder) CellStart(off int) error {
	b.curStart, b.buf = off, strings.Builder{}
	return nil
}

func (b *Builder) Data(p []byte) error {
	b.buf.Write(p)
	return nil
}

func (b *Builder) EscapedQuote(int) error {
	b.buf.WriteByte('"')
	return nil
}

func (b *Builder) CellEnd(off int, quoted bool) error {
	b.row = append(b.row, cell.Cell{
		Value: b.buf.String(), Quoted: quoted, Start: b.curStart, End: off,
	})
	return nil
}

func (b *Builder) RecordEnd(lfOff int) error {
	b.recNo++
	n := len(b.row)
	if b.completed == 0 {
		b.width = n
	} else if n != b.width {
		return b.fail(lfOff, ErrFieldCount)
	}
	if b.lim.MaxRecords > 0 && b.recNo > b.lim.MaxRecords {
		return b.fail(lfOff, ErrTooManyRecords)
	}
	b.complete = append(b.complete, append([]cell.Cell(nil), b.row...))
	b.completed++
	b.row = b.row[:0]
	return nil
}

func (b *Builder) fail(off int, err error) error {
	if b.finalErr == nil {
		b.finalErr = &Error{Err: err, Offset: off, Record: b.recNo, Field: len(b.row)}
	}
	return b.finalErr
}

// Stats 报告解析处理量。
type Stats struct{ BytesProcessed int64 }

// Parser 是单线程流式解析器，单实例非并发安全。
type Parser struct {
	lex *lexer.Lexer
	b   *Builder
	err error
}

// NewParser 创建流式解析器。
func NewParser(lim lexer.Limits) *Parser {
	b := NewBuilder(lim)
	return &Parser{lex: lexer.New(b, lim), b: b}
}

// Feed 喂入一段字节，可调用任意次；出错后返回同一错误。
func (p *Parser) Feed(q []byte) error {
	if p.err != nil {
		return p.err
	}
	return p.wrap(p.lex.Feed(q))
}

// Close 结束流并返回表、统计与错误。
func (p *Parser) Close() (*Table, Stats, error) {
	if p.err == nil {
		p.wrap(p.lex.Close())
	}
	return p.b.Table(), Stats{BytesProcessed: p.lex.ByteCount()}, p.err
}

// Parse 一次性解析。
func Parse(q []byte, lim lexer.Limits) (*Table, Stats, error) {
	p := NewParser(lim)
	_ = p.Feed(q)
	return p.Close()
}

func (p *Parser) wrap(err error) error {
	if err == nil {
		err = p.b.Err()
	}
	if e, ok := err.(*lexer.Error); ok {
		err = &Error{Err: e.Err, Offset: e.Offset, Record: e.Record, Field: e.Field}
	}
	if err != nil && p.err == nil {
		p.err = err
	}
	return p.err
}
