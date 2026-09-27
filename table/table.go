// Package table 把 lexer 的事件组装成表，做列数一致性与记录数上限校验。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

var (
	// ErrColumnMismatch：记录字段数与第一条记录不同。
	ErrColumnMismatch = errors.New("column count mismatch")
	// ErrTooManyRecords：记录数超过上限。
	ErrTooManyRecords = errors.New("too many records")
)

// Error 携带位置；可用 errors.Is 判别 Kind。
type Error struct {
	Kind           error
	Offset, Record int
	Field          int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d field %d)", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// Limits 在词法上限之外增加记录数上限；Header=true 时首条记录为表头。
type Limits struct {
	lexer.Limits
	MaxRecords int
	Header     bool
}

// Table 是组装结果；无表头时 Header 为 nil。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Assembler 消费「字段(slot 为记录内 1 基序号) + 行尾」事件流。
type Assembler struct {
	lim                              Limits
	fields                           []cell.Cell
	width                            int
	Header                           []cell.Cell
	Rows                             [][]cell.Cell
	term                             *Error
}

// NewAssembler 创建组装器。
func NewAssembler(lim Limits) *Assembler {
	return &Assembler{lim: lim, fields: make([]cell.Cell, 0, 8), rows: make([][]cell.Cell, 0)}
}

// Field 接收一个已完成字段。
func (a *Assembler) Field(c cell.Cell, slot int) error {
	if a.term != nil {
		return a.term
	}
	if slot != len(a.fields)+1 {
		a.fields = a.fields[:slot-1]
	}
	a.fields = append(a.fields, c)
	return nil
}

// EndRecord 结束一条记录；off 为行尾字节偏移。
func (a *Assembler) EndRecord(off int) error {
	if a.term != nil {
		return a.term
	}
	n := len(a.fields)
	if n == 0 {
		return nil
	}
	recNo := len(a.Rows) + 1
	if a.lim.Header && a.Header == nil {
		a.Header = append([]cell.Cell(nil), a.fields...)
		a.width = n
		a.fields = a.fields[:0]
		return nil
	}
	if a.width == 0 {
		a.width = n
	} else if n != a.width {
		a.term = &Error{Kind: ErrColumnMismatch, Offset: off, Record: recNo, Field: n}
		return a.term
	}
	if a.lim.MaxRecords > 0 && recNo > a.lim.MaxRecords {
		a.term = &Error{Kind: ErrTooManyRecords, Offset: off, Record: recNo, Field: n}
		return a.term
	}
	a.Rows = append(a.Rows, append([]cell.Cell(nil), a.fields...))
	a.fields = a.fields[:0]
	return nil
}

// Finish 结束输入；正常组装下 EOF 前的记录已由词法层显式闭合。
func (a *Assembler) Finish() error { return a.term }

// Table 返回已组装结果（出错时保留已产出的完整记录前缀）。
func (a *Assembler) Table() *Table { return &Table{Header: a.Header, Rows: a.Rows} }

// Parser 是单线程流式解析器；单实例非并发安全。
type Parser struct {
	lex  *lexer.Lexer
	asm  *Assembler
}

// NewParser 创建流式解析器。
func NewParser(lim Limits) *Parser {
	a := NewAssembler(lim)
	l := lexer.New(func(e lexer.Event) {
		if e.EndRecord {
			_ = a.EndRecord(e.Offset)
			return
		}
	_ = a.Field(e.C, e.Slot)
	}, lim.Limits)
	return &Parser{lex: l, asm: a}
}

// Feed 喂入字节块；进入终态后返回同一错误。
func (p *Parser) Feed(b []byte) error {
	if err := p.lex.Feed(b); err != nil {
		return err
	}
	return p.asm.Finish()
}

// Close 结束流。
func (p *Parser) Close() error {
	if err := p.lex.Close(); err != nil {
		return err
	}
	return p.asm.Finish()
}

// Table 返回当前结果。
func (p *Parser) Table() *Table { return p.asm.Table() }

// Parse 一次性解析整块输入。
func Parse(b []byte, lim Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(b); err != nil {
		return p.Table(), err
	}
	if err := p.Close(); err != nil {
		return p.Table(), err
	}
	return p.Table(), nil
}
