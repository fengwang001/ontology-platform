// Package lexer 是可暂停续传的逐字节 CSV 状态机。非并发安全。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

var (
	ErrQuote         = errors.New("unexpected quote in unquoted field")
	ErrGarbage       = errors.New("characters after closing quote")
	ErrUnclosedQuote = errors.New("unclosed quoted field at EOF")
	ErrBareCR        = errors.New("bare carriage return not followed by LF")
	ErrFieldTooLong  = errors.New("field exceeds MaxFieldBytes")
	ErrTooManyFields = errors.New("record exceeds MaxFields")
)

// Error 携带字节偏移(从0起)与记录号、字段号(均从1起)。
type Error struct {
	Kind                  error
	Offset, Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at offset %d record %d field %d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// Limits 为可配置上限，0 表示不限。
type Limits struct{ MaxFieldBytes, MaxFields int }

// Sink 接收词法事件；EndRecord 的 blank=true 表示空行。
type Sink interface {
	BeginRecord(offset int) error
	Field(cell.Cell)
	EndRecord(offset int, blank bool)
}

// 五个状态：字段开始/未引号中/引号中/刚见引号/CR待定；par 复用这些常量判定段末态。
const (
	StStart byte = iota
	StUnquoted
	StQuoted
	StQuotePending
	StCR
)

// Carry 是可暂停的状态机现场，半包续传与并行切分共用它。
type Carry struct {
	state                 byte
	val                   []byte
	quoted, recStarted    bool
	pos, fstart, rec, fld int
	steps                 int64
}

// NewCarry 返回记录边界上的现场，base 为下一字节全局偏移。
func NewCarry(base int) *Carry { return &Carry{pos: base, fstart: base, rec: 1, fld: 1} }
func (c *Carry) Steps() int64  { return c.steps }
func (c *Carry) Pos() int      { return c.pos }

// Snap 是跨段续接所需的现场快照。
type Snap struct {
	State            byte
	Val              string
	Quoted, Started  bool
	Rec, Fld, FStart int
}

func (c *Carry) Snap() Snap {
	return Snap{c.state, string(c.val), c.quoted, c.recStarted, c.rec, c.fld, c.fstart}
}

// Resume 按快照在 base（下一字节偏移）处恢复现场。
func Resume(base int, s Snap) *Carry {
	return &Carry{state: s.State, val: append([]byte(nil), s.Val...), quoted: s.Quoted,
		recStarted: s.Started, pos: base, fstart: s.FStart, rec: s.Rec, fld: s.Fld}
}

func fail(c *Carry, k error) error {
	return &Error{Kind: k, Offset: c.pos, Record: c.rec, Field: c.fld}
}
func (c *Carry) emit(s Sink) {
	s.Field(cell.Cell{Value: string(c.val), Quoted: c.quoted, Start: c.fstart, End: c.pos})
}

// Lexer 是流式外壳：多次 Feed 后 Close。单个实例非并发安全。
type Lexer struct {
	c    *Carry
	s    Sink
	lim  Limits
	dead error
}

func New(s Sink, lim Limits) *Lexer { return &Lexer{c: NewCarry(0), s: s, lim: lim} }

// Feed 追加输入；进入终态后永远返回同一错误。
func (l *Lexer) Feed(p []byte) error {
	if l.dead != nil {
		return l.dead
	}
	if err := Process(l.c, p, l.s, l.lim); err != nil {
		l.dead = err
	}
	return l.dead
}

// Close 结束流并裁决 EOF。
func (l *Lexer) Close() error {
	if l.dead != nil {
		return l.dead
	}
	if err := Finalize(l.c, l.s); err != nil {
		l.dead = err
	}
	return l.dead
}
func (l *Lexer) Steps() int64 { return l.c.steps }
