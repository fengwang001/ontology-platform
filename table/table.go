// Package table 把 lexer 的事件组装成表，并强制列数与上限。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

var (
	// ErrColumnCount 记录字段数与首条记录不一致。
	ErrColumnCount = errors.New("csv: record field count mismatch")
	// ErrFieldTooLong 单字段超过 MaxFieldBytes（判定点在 lexer）。
	ErrFieldTooLong = lexer.ErrFieldTooLong
	// ErrTooManyFields 单记录字段数超过 MaxFieldsPerRecord。
	ErrTooManyFields = errors.New("csv: too many fields in record")
	// ErrTooManyRecords 总记录数超过 MaxRecords。
	ErrTooManyRecords = errors.New("csv: too many records")
)

// Limits 是三类可配置上限；0 表示不限。
type Limits struct {
	MaxFieldBytes      int
	MaxFieldsPerRecord int
	MaxRecords         int
}

// PosError 带字节偏移、记录号、字段号（记录/字段从 1 起，偏移从 0 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

// Table 是解析结果。Header 为第一条记录（空流时为 nil）。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// RowsAll 返回包含表头在内的全部记录。
func (t *Table) RowsAll() [][]cell.Cell {
	if t == nil || t.Header == nil {
		return nil
	}
	return append([][]cell.Cell{t.Header}, t.Rows...)
}

// Builder 按全局坐标接收 lexer 事件并组装表，供流式与 par 共用。
// Builder 不做合并语义之外的假设；Err 返回后进入终态。
type Builder struct {
	lim    Limits
	tab    Table
	cur    []cell.Cell
	width  int
	recNo  int // 下一条将闭合记录的编号（从 1 起）
	fldNo  int // 当前记录已闭合字段数
	closed bool
	err    *PosError
}

// NewBuilder 创建 Builder。
func NewBuilder(lim Limits) *Builder {
	return &Builder{lim: lim, recNo: 1}
}

// Err 返回终态错误（nil 表示尚无错误）。
func (b *Builder) Err() *PosError { return b.err }

func (b *Builder) fail(err error, off int) *PosError {
	if b.err == nil {
		b.err = &PosError{Err: err, Offset: off, Record: b.recNo, Field: b.fldNo + 1}
		b.closed = true
	}
	return b.err
}

// Sink 是 lexer 事件入口。
func (b *Builder) Sink(ev lexer.Event) error {
	if b.closed {
		return b.err
	}
	switch ev.Kind {
	case lexer.KCell:
		b.fldNo++
		if b.lim.MaxFieldsPerRecord > 0 && b.fldNo > b.lim.MaxFieldsPerRecord {
			return b.fail(ErrTooManyFields, ev.Cell.Start)
		}
		b.cur = append(b.cur, ev.Cell)
	case lexer.KEOL:
		if b.width == 0 {
			b.width = len(b.cur)
		}
		if len(b.cur) != b.width {
			return b.fail(ErrColumnCount, ev.Off)
		}
		if b.lim.MaxRecords > 0 && b.recNo > b.lim.MaxRecords {
			return b.fail(ErrTooManyRecords, ev.Off)
		}
		rec := b.cur
		b.cur = nil
		b.fldNo = 0
		b.recNo++
		if b.tab.Header == nil {
			b.tab.Header = rec
		} else {
			b.tab.Rows = append(b.tab.Rows, rec)
		}
	case lexer.KErr:
		return b.fail(ev.Err, ev.Off)
	}
	return nil
}

// Finish 在字节流结束时调用：无尾换行的末记录在此闭合。
func (b *Builder) Finish(endOff int) *PosError {
	if b.closed {
		return b.err
	}
	if b.cur != nil {
		if b.width == 0 {
			b.width = len(b.cur)
		}
		if len(b.cur) != b.width {
			return b.fail(ErrColumnCount, endOff)
		}
		if b.lim.MaxRecords > 0 && b.recNo > b.lim.MaxRecords {
			return b.fail(ErrTooManyRecords, endOff)
		}
		if b.tab.Header == nil {
			b.tab.Header = b.cur
		} else {
			b.tab.Rows = append(b.tab.Rows, b.cur)
		}
		b.cur = nil
		b.recNo++
	}
	return nil
}

// Table 返回已产出的完整记录。
func (b *Builder) Table() *Table { return &b.tab }

// Parser 是增量流式解析器；单个实例非并发安全。
type Parser struct {
	m   *lexer.Machine
	b   *Builder
	off int
}

// NewParser 创建解析器。
func NewParser(lim Limits) *Parser {
	return &Parser{m: lexer.New().WithFieldLimit(lim.MaxFieldBytes), b: NewBuilder(lim)}
}

// Feed 追加一段字节，可调用任意次。
func (p *Parser) Feed(bin []byte) error {
	if p.b.Err() != nil {
		return p.b.Err()
	}
	if err := p.m.Run(bin, p.off, p.b.Sink); err != nil {
		return p.b.Err()
	}
	p.off += len(bin)
	return nil
}

// Close 结束输入。
func (p *Parser) Close() error {
	if p.b.Err() != nil {
		return p.b.Err()
	}
	_ = p.m.Finish(p.off, p.b.Sink)
	if err := p.b.Finish(p.off); err != nil {
		return err
	}
	return p.b.Err()
}

// Table 返回已成功产出的完整记录。
func (p *Parser) Table() *Table { return p.b.Table() }

// Processed 返回流式状态机处理过的字节数。
func (p *Parser) Processed() int { return p.m.Processed() }

// Parse 一次性解析。
func Parse(b []byte, lim Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(b); err != nil {
		return p.Table(), err
	}
	return p.Table(), p.Close()
}
