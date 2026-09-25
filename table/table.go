package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// 三类上限错误。
var (
	ErrFieldTooLarge = lexer.ErrFieldTooLarge
	ErrTooManyFields = errors.New("table: record exceeds max fields")
	ErrTooManyRows   = errors.New("table: table exceeds max records")
	ErrRaggedRow     = errors.New("table: record field count mismatch")
)

// Limits 为 0 的项表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Error 携带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type Error struct {
	Err    error
	Offset int
	Rec    int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Table 是解析结果。
type Table struct {
	Rows [][]cell.Cell
	lim  Limits
}

// Header 返回第一条记录（可能为 nil）。
func (t *Table) Header() []cell.Cell {
	if len(t.Rows) == 0 {
		return nil
	}
	return t.Rows[0]
}

// Builder 消费 lexer 事件，维护当前记录与上限；流式与 par 共用。
type Builder struct {
	T        Table
	cur      []cell.Cell
	lim      Limits
	final    *Error
}

func NewBuilder(lim Limits) *Builder {
	return &Builder{lim: lim, T: Table{lim: lim}}
}

func (b *Builder) fail(off int, err error) *Error {
	if b.final == nil {
		b.final = &Error{Err: err, Offset: off, Rec: len(b.T.Rows) + 1,
			Field: len(b.cur) + 1}
	}
	return b.final
}

// Field 处理一个字段事件（含合并后的全局字段）。
func (b *Builder) Field(c cell.Cell) *Error {
	if b.final != nil {
		return b.final
	}
	if b.lim.MaxFieldBytes > 0 && len(c.Value) > b.lim.MaxFieldBytes {
		return b.fail(c.Start+b.lim.MaxFieldBytes, ErrFieldTooLarge)
	}
	if b.lim.MaxFields > 0 && len(b.cur) >= b.lim.MaxFields {
		return b.fail(c.Start, ErrTooManyFields)
	}
	b.cur = append(b.cur, c)
	return nil
}

// Record 处理一条记录结束。
func (b *Builder) Record() *Error {
	if b.final != nil {
		return b.final
	}
	if len(b.T.Rows) > 0 && len(b.cur) != len(b.T.Rows[0]) {
		return b.fail(len(b.T.Rows[0])+1, ErrRaggedRow)
	}
	if b.lim.MaxRecords > 0 && len(b.T.Rows) >= b.lim.MaxRecords {
		return b.fail(0, ErrTooManyRows)
	}
	row := b.cur
	b.cur = nil
	b.T.Rows = append(b.T.Rows, row)
	return nil
}

// Parser 是单线程流式解析器。单实例非并发安全。
type Parser struct {
	m *lexer.Machine
	b *Builder
}

func NewParser(lim Limits) *Parser {
	b := NewBuilder(lim)
	m := &lexer.Machine{MaxField: lim.MaxFieldBytes}
	m.OnField = func(e lexer.FieldEvt) error {
		if err := b.Field(e.C); err != nil {
			return err.Err
		}
		return nil
	}
	m.OnRecord = func(lexer.RecEvt) error {
		if err := b.Record(); err != nil {
			return err.Err
		}
		return nil
	}
	return &Parser{m: m, b: b}
}

// Feed 送入一段字节。
func (p *Parser) Feed(q []byte) error {
	if err := p.m.Feed(q); err != nil {
		return p.b.final
	}
	return nil
}

// Close 结束流并返回表。
func (p *Parser) Close() (*Table, error) {
	if err := p.m.Close(); err != nil {
		return nil, p.b.final
	}
	return &p.b.T, nil
}

// Parse 一次性解析。
func Parse(q []byte, lim Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(q); err != nil {
		return nil, err
	}
	return p.Close()
}
