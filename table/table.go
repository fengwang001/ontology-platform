// Package table 把词法事件组装成记录表：表头、列数一致性、行列定位。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnCount 为记录列数与首条记录不一致的可判定错误。
var ErrColumnCount = errors.New("table: record column count mismatch")

// Table 为解析结果。Header 为首条记录，Rows 为其余记录。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Width 返回首条记录的列数。
func (t *Table) Width() int { return len(t.Header) }

// Records 返回全部记录（含表头）。
func (t *Table) Records() [][]cell.Cell {
	out := make([][]cell.Cell, 0, 1+len(t.Rows))
	if t.Header != nil {
		out = append(out, t.Header)
	}
	return append(out, t.Rows...)
}

// Error 为表级错误，包装 lexer 错误并保证四类错误可判定（errors.Is）。
type Error struct {
	Kind   error
	Offset int64
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type builder struct {
	t       *Table
	cur     []cell.Cell
	lim     cell.Limits
	header  bool
	lastOff int64
	fatal   *Error
}

func (b *builder) Cell(c cell.Cell, field int) {
	b.cur = append(b.cur, c)
	b.lastOff = c.End
}

func (b *builder) EndRecord(recNo int) {
	if b.fatal != nil {
		return
	}
	if !b.header {
		b.t.Header, b.cur, b.header = b.cur, nil, true
		return
	}
	if b.t.Width() != len(b.cur) {
		b.fatal = &Error{Kind: ErrColumnCount, Offset: b.lastOff, Record: recNo, Field: len(b.cur)}
		return
	}
	b.t.Rows = append(b.t.Rows, b.cur)
	b.cur = nil
}

// Streamer 是半包续传的表级解析器。单实例非并发安全。
type Streamer struct {
	lex *lexer.Lexer
	b   *builder
}

// NewStream 创建流式解析器。
func NewStream(lim cell.Limits) *Streamer {
	b := &builder{t: &Table{}, lim: lim}
	return &Streamer{lex: lexer.New(b, lim), b: b}
}

// Feed 送入一段输入。
func (s *Streamer) Feed(p []byte) error { return s.wrap(s.lex.Feed(p)) }

// Close 结束输入。
func (s *Streamer) Close() error {
	if err := s.wrap(s.lex.Close()); err != nil {
		return err
	}
	return s.wrap(nil)
}

func (s *Streamer) wrap(err error) error {
	if s.b.fatal != nil && err == nil {
		return s.b.fatal
	}
	if le, ok := err.(*lexer.Error); ok {
		return &Error{Kind: le.Kind, Offset: le.Offset, Record: le.Record, Field: le.Field}
	}
	return err
}

// Table 返回当前已产出的完整记录。
func (s *Streamer) Table() *Table { return s.b.t }

// BytesSeen 返回词法状态机处理过的字节总数。
func (s *Streamer) BytesSeen() int64 { return s.lex.BytesSeen() }

// Parse 一次性解析完整输入。
func Parse(p []byte, lim cell.Limits) (*Table, error) {
	s := NewStream(lim)
	if err := s.Feed(p); err != nil {
		return s.Table(), err
	}
	if err := s.Close(); err != nil {
		return s.Table(), err
	}
	return s.Table(), nil
}
