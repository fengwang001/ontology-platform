// Package table 把 lexer 事件组装成表，强制列数一致与可配置上限。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

// 三类上限错误与列数不一致错误，彼此可判定。
var (
	ErrTooManyFields = errors.New("record exceeds max fields")
	ErrTooManyRows   = errors.New("table exceeds max records")
	ErrColumnCount   = errors.New("record field count differs from header")
)

// ErrFieldTooLarge 复用 lexer 哨兵：错误在第一个超限字节处由词法层抛出。
var ErrFieldTooLarge = lexer.ErrFieldTooLarge

// Limits 为 0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Error 带位置信息。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

// Table 同时实现 lexer.Sink。
type Table struct {
	Limits Limits
	Rows   []cell.Record

	cur      cell.Record
	ncols    int
	fieldLen int
	terminal error
}

// Reset 清空状态（复用实例，验证互不串扰）。
func (t *Table) Reset() { *t = Table{Limits: t.Limits} }

// Terminal 返回终态错误。
func (t *Table) Terminal() error { return t.terminal }

func (t *Table) die(e error, off, rec, fld int) error {
	err := &Error{Err: e, Offset: off, Record: rec, Field: fld}
	t.terminal = err
	return err
}

// AllowBytes 实现 lexer.Gate：内容字节到达前即检查字段上限。
func (t *Table) AllowBytes(n int) error {
	if t.Limits.MaxFieldBytes > 0 && n > t.Limits.MaxFieldBytes {
		return ErrFieldTooLarge
	}
	t.fieldLen = n
	return nil
}

// FieldLen 返回当前字段已接收的内容字节数（par 拼接复用）。
func (t *Table) FieldLen() int { return t.fieldLen }

// NoteBytes 供 par 回放内容增量时维护字段长度。
func (t *Table) NoteBytes(n int) { t.fieldLen = n }

// Cell 实现 lexer.Sink。
func (t *Table) Cell(c cell.Cell, record, field int) {
	if t.terminal != nil {
		return
	}
	if t.Limits.MaxFields > 0 && field > t.Limits.MaxFields {
		t.die(ErrTooManyFields, c.Start, record, field)
		return
	}
	if t.Limits.MaxFieldBytes > 0 && len(c.Value) > t.Limits.MaxFieldBytes {
		t.die(ErrFieldTooLarge, c.Start, record, field)
		return
	}
	t.cur = append(t.cur, c)
	t.fieldLen = 0
}

// EndRecord 实现 lexer.Sink。
func (t *Table) EndRecord(record int) {
	if t.terminal != nil {
		return
	}
	if t.Limits.MaxRecords > 0 && len(t.Rows) >= t.Limits.MaxRecords {
		last := 0
		if n := len(t.cur); n > 0 {
			last = t.cur[n-1].Start
		}
		t.die(ErrTooManyRows, last, record, 1)
		return
	}
	if t.ncols == 0 {
		t.ncols = len(t.cur)
	} else if len(t.cur) != t.ncols {
		last := 0
		if n := len(t.cur); n > 0 {
			last = t.cur[n-1].Start
		}
		t.die(ErrColumnCount, last, record, len(t.cur)+1)
		return
	}
	t.Rows = append(t.Rows, t.cur)
	t.cur = nil
	t.fieldLen = 0
}

// Parser 是流式入口：多次 Feed，最后 Close。
type Parser struct {
	tab *Table
	lx  *lexer.Lexer
}

// NewParser 创建流式解析器。
func NewParser(lim Limits) *Parser {
	t := &Table{Limits: lim}
	return &Parser{tab: t, lx: lexer.New(t)}
}

// Feed 送入字节。
func (p *Parser) Feed(b []byte) error {
	if err := p.lx.Feed(b); err != nil {
		return p.tab.terminalOr(err)
	}
	return nil
}

// Close 结束输入。
func (p *Parser) Close() error {
	if err := p.lx.Close(); err != nil {
		return p.tab.terminalOr(err)
	}
	return p.tab.terminal
}

// Table 返回已产出的表（错误时保留完整记录前缀）。
func (p *Parser) Table() *Table { return p.tab }

// Processed 返回词法处理字节数。
func (p *Parser) Processed() int { return p.lx.Processed() }

func (t *Table) terminalOr(lexErr error) error {
	if t.terminal != nil {
		return t.terminal
	}
	return lexErr
}

// Parse 一次性解析。
func Parse(in []byte, lim Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(in); err != nil {
		return p.Table(), err
	}
	if err := p.Close(); err != nil {
		return p.Table(), err
	}
	return p.Table(), nil
}
