// Package table 把词法事件组装成表：列数一致性、表头与行列定位。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

// ErrFieldCount 为记录列数与首条记录不一致；ErrTooManyRecords 为记录数超限。
var (
	ErrFieldCount     = errors.New("field count differs from first record")
	ErrTooManyRecords = errors.New("table exceeds MaxRecords")
)

// Limits 增加总记录数上限，0 表示不限。
type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }

// Row 是一条记录。
type Row struct{ Cells []cell.Cell }

// Table 是解析结果；Header 为 nil 表示无表头。
type Table struct {
	Header []string
	Rows   []Row
}

// Error 与 lexer.Error 同形状，额外标注 Kind，四类语法错误可经 Unwrap 判定。
type Error struct {
	Kind          error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at offset %d record %d field %d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// Builder 是 lexer.Sink，逐事件建表；列数不符即终止。
type Builder struct {
	lim     Limits
	cur     Row
	rows    []Row
	ncol    int
	dead    error
	pending bool // BeginRecord 已触发、字段尚未计数
}

// NewBuilder 构造 Builder。
func NewBuilder(lim Limits) *Builder { return &Builder{lim: lim} }

// BeginRecord 在每条真实记录第一个字节到达时检查总记录数上限。
func (b *Builder) BeginRecord(offset int) error {
	b.pending = true
	if b.lim.MaxRecords > 0 && len(b.rows)+1 > b.lim.MaxRecords {
		return &Error{Kind: ErrTooManyRecords, Offset: offset, Record: len(b.rows) + 1, Field: 1}
	}
	return nil
}

// Field 实现 lexer.Sink。
func (b *Builder) Field(c cell.Cell) {
	b.pending = false
	b.cur.Cells = append(b.cur.Cells, c)
}

// EndRecord 实现 lexer.Sink；空行跳过。
func (b *Builder) EndRecord(offset int, blank bool) {
	if blank {
		b.cur = Row{}
		return
	}
	n := len(b.rows) + 1
	if b.ncol == 0 {
		b.ncol = len(b.cur.Cells)
	} else if len(b.cur.Cells) != b.ncol {
		b.dead = &Error{Kind: ErrFieldCount, Offset: offset, Record: n, Field: len(b.cur.Cells)}
		return
	}
	row := b.cur
	row.Cells = append([]cell.Cell(nil), row.Cells...)
	b.rows = append(b.rows, row)
	b.cur = Row{}
}

// Table 返回已组装的表；header=true 时首行作为表头。
func (b *Builder) Table(header bool) *Table {
	t := &Table{Rows: b.rows}
	if header && len(b.rows) > 0 {
		t.Header = make([]string, len(b.rows[0].Cells))
		for i, c := range b.rows[0].Cells {
			t.Header[i] = c.Value
		}
		t.Rows = b.rows[1:]
	}
	return t
}

// Parse 一次性解析 CSV；header 决定首记录是否为表头。
func Parse(p []byte, header bool, lim Limits) (*Table, error) {
	b := NewBuilder(lim)
	l := lexer.New(b, lexer.Limits{MaxFieldBytes: lim.MaxFieldBytes, MaxFields: lim.MaxFields})
	if err := l.Feed(p); err != nil {
		return nil, err
	}
	if err := l.Close(); err != nil {
		return nil, err
	}
	if b.dead != nil {
		return nil, b.dead
	}
	return b.Table(header), nil
}

// ParseFeed 供需要半包续传/并行拼接的调用方直接驱动 Builder。
type ParseFeed struct {
	b *Builder
	l *lexer.Lexer
}

// NewFeed 构造流式解析。
func NewFeed(lim Limits) *ParseFeed {
	b := NewBuilder(lim)
	return &ParseFeed{b: b, l: lexer.New(b, lexer.Limits{MaxFieldBytes: lim.MaxFieldBytes, MaxFields: lim.MaxFields})}
}
func (f *ParseFeed) Feed(p []byte) error {
	if err := f.l.Feed(p); err != nil {
		return err
	}
	return f.b.dead
}
func (f *ParseFeed) Close(header bool) (*Table, error) {
	if err := f.l.Close(); err != nil {
		return nil, err
	}
	if f.b.dead != nil {
		return nil, f.b.dead
	}
	return f.b.Table(header), nil
}
func (f *ParseFeed) Steps() int64 { return f.l.Steps() }
