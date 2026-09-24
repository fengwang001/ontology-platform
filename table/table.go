// Package table 把 lexer 事件组装成表：表头、行、列数一致性、行列定位。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnMismatch 在记录字段数与第一条记录不一致时返回。
var ErrColumnMismatch = errors.New("table: record field count does not match header")

// Error 是列数错误，带位置（第一条不一致字段/记录的位置）。
type Error struct {
	Err    error
	Byte   int
	Record int
	Field  int
	Want   int
	Got    int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d (want %d, got %d)",
		e.Err, e.Byte, e.Record, e.Field, e.Want, e.Got)
}
func (e *Error) Unwrap() error { return e.Err }

// Table 是解析成功的完整表。Header 为第一条记录，Rows 为其余记录。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Records 返回全部记录（含表头）。
func (t *Table) Records() [][]cell.Cell {
	out := make([][]cell.Cell, 0, 1+len(t.Rows))
	out = append(out, t.Header)
	out = append(out, t.Rows...)
	return out
}

// Builder 是 lexer.Emit 的增量组装器。单个实例非并发安全。
type Builder struct {
	Table Table
	cur   []cell.Cell
	ncol  int
	err   error
}

// NewBuilder 创建组装器，配合 lexer.New(cfg, b.Emit()) 使用。
func NewBuilder() *Builder { return &Builder{} }

// Emit 返回 lexer 回调。
func (b *Builder) Emit() lexer.Emit {
	return func(c cell.Cell, recordEnd, blank bool) {
		if b.err != nil || blank {
			return
		}
		if recordEnd {
			rec := b.cur
			b.cur = nil
			if b.Table.Header == nil {
				b.Table.Header = rec
				b.ncol = len(rec)
			} else {
				if len(rec) != b.ncol {
					last := rec[len(rec)-1]
					b.err = &Error{Err: ErrColumnMismatch, Byte: last.Start,
						Record: len(b.Table.Rows) + 2, Field: len(rec), Want: b.ncol, Got: len(rec)}
					return
				}
				b.Table.Rows = append(b.Table.Rows, rec)
			}
			return
		}
		b.cur = append(b.cur, c)
	}
}

// Err 返回组装过程中的列数错误。
func (b *Builder) Err() error { return b.err }

// Parse 是一次性便捷解析；返回 lexer/table 的首个错误。
func Parse(p []byte, cfg lexer.Config) (*Table, error) {
	b := NewBuilder()
	l := lexer.New(cfg, b.Emit())
	if err := l.Feed(p); err != nil {
		return nil, err
	}
	if err := l.Close(); err != nil {
		return nil, err
	}
	return &b.Table, b.Err()
}
