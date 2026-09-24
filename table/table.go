// Package table 把 lexer 的字段/记录事件组装成带表头的表。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnCount 表示记录列数与首条记录不一致。
var ErrColumnCount = errors.New("record column count mismatch")

// Table 是一次解析的完整结果；首条记录为表头。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Width 返回表头列数（无记录时为 0）。
func (t *Table) Width() int { return len(t.Header) }

// Records 返回含表头在内的全部记录。
func (t *Table) Records() [][]cell.Cell {
	out := make([][]cell.Cell, 0, 1+len(t.Rows))
	out = append(out, t.Header)
	return append(out, t.Rows...)
}

type sink struct {
	t   *Table
	cur []cell.Cell
}

func (s *sink) Field(c cell.Cell) error {
	s.cur = append(s.cur, c)
	return nil
}

func (s *sink) EndRecord(offset int) error {
	rec := s.cur
	s.cur = nil
	if s.t.Header == nil {
		s.t.Header = rec
		return nil
	}
	if len(rec) != len(s.t.Header) {
		return &lexer.Error{Err: ErrColumnCount, Offset: offset, Record: 1 + len(s.t.Rows), Field: len(rec) + 1}
	}
	s.t.Rows = append(s.t.Rows, rec)
	return nil
}

// Parse 流式解析整块输入（便捷入口）。
func Parse(p []byte, lim lexer.Limits) (*Table, error) {
	t := &Table{}
	s := &sink{t: t}
	lx := lexer.New(s, lim)
	if err := lx.Feed(p); err != nil {
		return t, err
	}
	if err := lx.Close(); err != nil {
		return t, err
	}
	return t, nil
}
