// Package table 把记录组装成表：列数一致性、表头、行号列号定位。
// 单个 Parser 实例不要求并发安全。
package table

import (
	"errors"
	"fmt"
	"slices"

	"ontology/cell"
	"ontology/lexer"
)

var ErrInconsistentColumns = errors.New("table: inconsistent column count")

// Error 为列数不一致错误：Offset 为该记录首字段偏移，Record 为记录号，
// Field 为该记录实际字段数（均从 1 起，偏移从 0 起）。
type Error struct {
	Offset        int64
	Record, Field int64
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v (offset %d, record %d, field %d)", ErrInconsistentColumns, e.Offset, e.Record, e.Field)
}

func (e *Error) Unwrap() error { return ErrInconsistentColumns }

// Table 第一张记录为表头，其余为数据行。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
	width  int
}

func (t *Table) NCol() int { return t.width }

func (t *Table) AddRecord(rec []cell.Cell) error { return nil }

func (t *Table) Equal(o *Table) bool {
	eq := slices.EqualFunc[[]cell.Cell]
	return eq(t.Header, o.Header, cell.Cell.Equal) && t.width == o.width &&
		slices.EqualFunc(t.Rows, o.Rows, func(a, b []cell.Cell) bool { return eq(a, b, cell.Cell.Equal) })
}

// Parser 是流式解析器：Feed 可任意多次调用，Close 结束。
type Parser struct {
	t   *Table
	rec []cell.Cell
	err error
	lx  *lexer.Lexer
}

func NewParser(lim lexer.Limits) *Parser {
	p := &Parser{t: &Table{}}
	p.lx = lexer.New(p.on, lim)
	return p
}

func (p *Parser) on(ev lexer.Event) error { return nil }

func (p *Parser) Feed(b []byte) error { return nil }

func (p *Parser) Close() error { return nil }

func (p *Parser) Table() *Table { return p.t }

// Parse 一次性解析完整缓冲区。
func Parse(buf []byte, lim lexer.Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(buf); err != nil {
		return p.Table(), err
	}
	return p.Table(), p.Close()
}
