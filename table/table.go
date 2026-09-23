// Package table 把 lexer 事件组装成记录与表头，并校验列数一致性。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Table 是解析结果。Header 为第一条记录（可为 nil）。
type Table struct {
	Header cell.Record
	Rows   []cell.Record

	cur  cell.Record
	ncol int
	lim  lexer.Limits
	seen bool
}

// NewTable 创建带上限的组装器（0 表示不限）。
func NewTable(lim lexer.Limits) *Table { return &Table{lim: lim} }

// OnCell 实现 lexer.Handler。
func (t *Table) OnCell(c cell.Cell) error {
	if t.lim.MaxFieldsPerRecord > 0 && t.seen && len(t.cur)+1 > t.ncol {
		// 列数不一致优先报 ErrFieldCount；上限在 OnRecord 终判。
	}
	t.cur = append(t.cur, c)
	return nil
}

// OnRecord 实现 lexer.Handler：空记录（空行）被跳过。
func (t *Table) OnRecord() error {
	rec := t.cur
	t.cur = nil
	if rec == nil {
		return nil
}
	if !t.seen {
		t.seen, t.ncol = true, len(rec)
		if t.lim.MaxFieldsPerRecord > 0 && len(rec) > t.lim.MaxFieldsPerRecord {
			last := rec[len(rec)-1]
			return &lexer.PosError{Err: lexer.ErrTooManyFields, Offset: last.End,
				Record: 1, Field: len(rec)}
		}
		t.Header = rec
		return nil
	}
	if len(rec) != t.ncol {
		last := rec[len(rec)-1]
		return &lexer.PosError{Err: lexer.ErrFieldCount, Offset: last.End,
			Record: len(t.Rows) + 2, Field: len(rec)}
	}
	if t.lim.MaxFieldsPerRecord > 0 && len(rec) > t.lim.MaxFieldsPerRecord {
		last := rec[len(rec)-1]
		return &lexer.PosError{Err: lexer.ErrTooManyFields, Offset: last.End,
			Record: len(t.Rows) + 2, Field: len(rec)}
	}
	if t.lim.MaxRecords > 0 && len(t.Rows)+1 >= t.lim.MaxRecords {
		last := rec[len(rec)-1]
		return &lexer.PosError{Err: lexer.ErrTooManyRecords, Offset: last.End,
			Record: len(t.Rows) + 2, Field: len(rec)}
	}
	t.Rows = append(t.Rows, rec)
	return nil
}

// Records 返回含表头的全部记录（表头在前）。
func (t *Table) Records() []cell.Record {
	if !t.seen {
		return nil
	}
	return append([]cell.Record{t.Header}, t.Rows...)
}

// Parse 一次性解析 buf。
func Parse(buf []byte, lim lexer.Limits) (*Table, error) {
	t := NewTable(lim)
	lx := lexer.New(t, lim)
	if err := lx.Feed(buf); err != nil {
		return t, err
	}
	if err := lx.Close(); err != nil {
		return t, err
	}
	return t, nil
}

// Stream 是多次 Feed 的流式解析器，Close 后取 Table。
type Stream struct {
	t *Table
	l *lexer.Lexer
}

// NewStream 创建流式解析器。
func NewStream(lim lexer.Limits) *Stream {
	t := NewTable(lim)
	return &Stream{t: t, l: lexer.New(t, lim)}
}

// Feed 喂入字节。
func (s *Stream) Feed(p []byte) error { return s.l.Feed(p) }

// Close 结束并返回组装好的表。
func (s *Stream) Close() (*Table, error) {
	if err := s.l.Close(); err != nil {
		return s.t, err
	}
	return s.t, nil
}
