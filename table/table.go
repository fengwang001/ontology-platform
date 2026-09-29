// Package table 把词法事件组装成表：列数一致性、表头、行列定位。
// 单实例非并发安全。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Config 为表级配置。
type Config struct {
	Lexer      lexer.Config
	MaxRecords int // 0 表示不限制
}

// Builder 从事件流组装记录；MaxRecords 超限时立刻报错并进入终态。
type Builder struct {
	maxRecords int
	width      int
	hasWidth   bool
	cur        cell.Record
	rows       []cell.Record
	err        error
}

// NewBuilder 创建组装器。
func NewBuilder(maxRecords int) *Builder { return &Builder{maxRecords: maxRecords} }

// Add 消费一个事件；pos 为事件对应的全局字节偏移（用于错误定位）。
func (b *Builder) Add(ev lexer.Event, pos int) error {
	if b.err != nil {
		return b.err
	}
	if !ev.End {
		b.cur = append(b.cur, ev.Cell)
		return nil
	}
	recNo := len(b.rows) + 1
	if !b.hasWidth {
		b.width, b.hasWidth = len(b.cur), true
	} else if len(b.cur) != b.width {
		b.err = &cell.Error{Kind: cell.ErrFieldCount, Offset: pos, Record: recNo, Field: len(b.cur)}
		return b.err
	}
	if b.maxRecords > 0 && recNo > b.maxRecords {
		b.err = &cell.Error{Kind: cell.ErrTooManyRecords, Offset: pos, Record: recNo, Field: 1}
		return b.err
	}
	b.rows = append(b.rows, b.cur)
	b.cur = nil
	return nil
}

// Rows 返回已产出的完整记录（出错后仍保留前缀）。
func (b *Builder) Rows() []cell.Record { return b.rows }

// Table 是流式解析器：Feed 任意多次，Close 取结果。
type Table struct {
	lx     *lexer.Lexer
	b      *Builder
	closed bool
	err    error
}

// New 创建流式解析器。
func New(cfg Config) *Table {
	return &Table{lx: lexer.New(cfg.Lexer, 0), b: NewBuilder(cfg.MaxRecords)}
}

func (t *Table) drain() {
	for _, ev := range t.lx.Events() {
		if err := t.b.Add(ev, t.lx.Pos()); err != nil && t.err == nil {
			t.err = err
		}
	}
}

// Feed 喂入一段字节，可调用任意多次；出错后进入终态。
func (t *Table) Feed(p []byte) error {
	if t.err != nil {
		return t.err
	}
	if t.closed {
		t.err = lexer.ErrClosed
		return t.err
	}
	if err := t.lx.Feed(p); err != nil {
		t.drain()
		t.err = err
		return err
	}
	t.drain()
	return t.err
}

// Close 结束输入并返回解析出的全部记录。
func (t *Table) Close() ([]cell.Record, error) {
	if t.err != nil {
		return t.rows(), t.err
	}
	if t.closed {
		return t.rows(), lexer.ErrClosed
	}
	t.closed = true
	if err := t.lx.Close(); err != nil {
		t.drain()
		t.err = err
		return t.rows(), err
	}
	t.drain()
	return t.rows(), t.err
}

func (t *Table) rows() []cell.Record { return t.b.Rows() }

// Header 返回表头（第一条记录），无记录时返回 nil。
func Header(rows []cell.Record) cell.Record {
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

// Row 返回第 i 条记录（0 起）及其 1 起的行号。
func Row(rows []cell.Record, i int) (cell.Record, int) { return rows[i], i + 1 }
