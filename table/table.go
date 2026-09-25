// Package table 把 lexer 事件组装成带表头与行列定位的表。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

var (
// ErrTooManyFields 单记录字段数超限。
ErrTooManyFields = errors.New("table: record exceeds max fields")
// ErrTooManyRecords 总记录数超限。
ErrTooManyRecords = errors.New("table: too many records")
// ErrRagged 记录列数与第一条不一致。
ErrRagged = errors.New("table: record field count mismatch")
)

// Limits 为可配置上限；0 表示不限。
type Limits struct {
	MaxFieldBytes  int
	MaxRecordFields int
	MaxRecords     int
}

// Table 是解析结果。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Builder 实现 lexer.Handler，并驱动流式解析。
type Builder struct {
	tab     Table
	lim     Limits
	lx      *lexer.Lexer
	cur     []cell.Cell
	fatal   error
	committed int
}

// New 创建流式解析器。
func New(lim Limits) *Builder {
	b := &Builder{lim: lim}
	b.lx = lexer.New(b, lim.MaxFieldBytes, 0, lexer.Init{})
	return b
}

// Table 返回已产出的完整表（含表头）。
func (b *Builder) Table() Table { return b.tab }

// Lexer 暴露内部状态机（供 par 复用限制）。
func (b *Builder) Lexer() *lexer.Lexer { return b.lx }

func (b *Builder) fail(err error) error {
	if b.fatal == nil {
		b.fatal = err
	}
	return b.fatal
}

// Field 实现 lexer.Handler。
func (b *Builder) Field(c cell.Cell) error {
	if b.lim.MaxRecordFields > 0 && len(b.cur) >= b.lim.MaxRecordFields {
		return b.fail(&lexer.PosError{Err: ErrTooManyFields, Offset: c.Start, Record: len(b.tab.Rows) + 2, Field: len(b.cur) + 1})
	}
	b.cur = append(b.cur, c)
	return nil
}

// EndRecord 实现 lexer.Handler。
func (b *Builder) EndRecord() error {
	rec := append([]cell.Cell(nil), b.cur...)
	b.cur = b.cur[:0]
	if len(b.tab.Header) == 0 && len(b.tab.Rows) == 0 {
		b.tab.Header = rec
		return nil
	}
	if b.lim.MaxRecords > 0 && len(b.tab.Rows) >= b.lim.MaxRecords {
		return b.fail(&lexer.PosError{Err: ErrTooManyRecords, Offset: rec[0].Start, Record: len(b.tab.Rows) + 2, Field: 1})
	}
	if len(rec) != len(b.tab.Header) {
		return b.fail(&lexer.PosError{Err: ErrRagged, Offset: rec[0].Start, Record: len(b.tab.Rows) + 2, Field: len(rec)})
	}
	b.tab.Rows = append(b.tab.Rows, rec)
	return nil
}

// Feed 续传一段字节。
func (b *Builder) Feed(p []byte) error {
	if b.fatal != nil {
		return b.fatal
	}
	if err := b.lx.Feed(p); err != nil {
		return b.fail(err)
	}
	return nil
}

// Close 结束流。
func (b *Builder) Close() error {
	if b.fatal != nil {
		return b.fatal
	}
	if err := b.lx.Close(); err != nil {
		return b.fail(err)
	}
	return nil
}

// Parse 一次性解析便捷函数。
func Parse(p []byte, lim Limits) (Table, error) {
	b := New(lim)
	if err := b.Feed(p); err != nil {
		return b.Table(), err
	}
	if err := b.Close(); err != nil {
		return b.Table(), err
	}
	return b.Table(), nil
}

// ReplayField 供 par 回放一个词法字段，执行字段数上限检查。
func (b *Builder) ReplayField(c cell.Cell) error { return b.Field(c) }

// ReplayEndRecord 供 par 回放记录边界，执行列数与记录数检查。
func (b *Builder) ReplayEndRecord() error { return b.EndRecord() }

// ReplayError 供 par 回放词法错误，返回带真实记录/字段号的位置错误。
func (b *Builder) ReplayError(err *lexer.PosError) error { return b.fail(err) }
