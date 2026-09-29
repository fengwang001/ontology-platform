// Package table 把 lexer 事件装配成记录，负责列数一致与三类上限。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

// Limits 为可配上限；0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// 上限与列数哨兵错误；语法错误沿用 lexer 哨兵。
var (
	ErrFieldTooLarge  = errors.New("table: field exceeds max bytes")
	ErrTooManyFields  = errors.New("table: too many fields in record")
	ErrTooManyRecords = errors.New("table: too many records")
	ErrFieldCount     = errors.New("table: field count mismatch")
)

// Error 携带类别、字节偏移、记录号、字段号（号从 1 起，偏移从 0 起）。
type Error struct {
	Kind   error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d",
		e.Kind, e.Offset, e.Record, e.Field)
}

func (e *Error) Unwrap() error { return e.Kind }

// Table 是装配结果与流式装配器（实现 lexer.Sink）。
type Table struct {
	Records []cell.Record
	Lim     Limits
	Err     *Error

	src       []byte
	cur       cell.Cell
	rec       cell.Record
	nfield    int  // 当前记录已开始的字段数
	nrec      int  // 已落盘记录数
	runOpen   bool // 有未结算的原文段
	runSt     int  // 原文段起点
	runBytes  int  // 自上次检查以来新增的原文段长度计数
	fieldSize int  // 当前字段已解码字节数
}

// New 构造以 src 为原文（零拷贝取值）的装配器。
func New(src []byte, lim Limits) *Table { return &Table{src: src, Lim: lim} }

// Parse 一次性解析（内部仍走 Feed/Close 流式路径）。
func Parse(src []byte, lim Limits) (*Table, error) {
	t := New(src, lim)
	l := lexer.New(t)
	err := l.Feed(src, 0)
	if err == nil {
		err = l.Close()
	}
	return t, t.Err
}

func (t *Table) fail(kind error, off int) {
	if t.Err != nil {
		return
	}
	t.Err = &Error{Kind: kind, Offset: off,
		Record: t.nrec + 1, Field: t.nfield}
}

func (t *Table) addSize(delta, off int) bool {
	t.fieldSize += delta
	if t.Lim.MaxFieldBytes > 0 && t.fieldSize > t.Lim.MaxFieldBytes {
		t.fail(ErrFieldTooLarge, off)
		return false
	}
	return true
}

func (t *Table) endRun(at int) {
	if !t.runOpen {
		return
	}
	t.runOpen = false
	if at > t.runSt {
		t.cur.Value += string(t.src[t.runSt:at])
	}
}

// Event 实现 lexer.Sink。
func (t *Table) Event(e lexer.Ev) {
	if t.Err != nil {
		return
	}
	switch e.K {
	case lexer.EvStart:
		t.nfield++
		if t.Lim.MaxFields > 0 && t.nfield > t.Lim.MaxFields {
			t.fail(ErrTooManyFields, e.Off)
			return
		}
		t.cur = cell.Cell{Start: e.Off, End: e.Off}
		t.fieldSize = 0
	case lexer.EvQuoteOpen:
		t.cur.Quoted = true
		t.cur.Start = e.Off
	case lexer.EvQuoteClose:
		t.endRun(e.Off)
		t.cur.End = e.Off + 1
	case lexer.EvRun:
		if !t.runOpen {
			t.runOpen, t.runSt = true, e.Off
		}
		if !t.addSize(1, e.Off) {
			return
		}
	case lexer.EvQChar:
		t.endRun(e.Off)
		t.cur.Value += "\""
		t.runOpen, t.runSt = true, e.Off+2
		if !t.addSize(1, e.Off) {
			return
		}
	case lexer.EvFieldEnd:
		t.endRun(e.Off)
		if !t.cur.Quoted {
			t.cur.End = e.Off
		}
		t.rec = append(t.rec, t.cur)
		t.cur = cell.Cell{}
	case lexer.EvBlank:
		t.nfield, t.rec = 0, nil
	case lexer.EvRecEnd:
		t.endRun(e.Off)
		if !t.cur.Quoted {
			t.cur.End = e.Off
		}
		t.rec = append(t.rec, t.cur)
		t.finish(e.Off)
	case lexer.EvLoneCR:
		t.fail(lexer.ErrLoneCR, e.Off)
	case lexer.EvUnclosed:
		t.fail(lexer.ErrUnclosedQuote, e.Off)
	case lexer.EvFlush:
		t.endRun(len(t.src))
		if !t.cur.Quoted {
			t.cur.End = len(t.src)
		} else if t.cur.End == 0 {
			t.cur.End = len(t.src)
		}
		t.rec = append(t.rec, t.cur)
		t.finish(len(t.src))
	}
}

func (t *Table) finish(off int) {
	if t.nrec > 0 || len(t.Records) > 0 {
		if len(t.rec) != len(t.Records[0]) {
			t.fail(ErrFieldCount, off)
			return
		}
	}
	t.Records = append(t.Records, t.rec)
	t.nrec++
	t.rec, t.nfield = nil, 0
	if t.Lim.MaxRecords > 0 && t.nrec > t.Lim.MaxRecords {
		t.fail(ErrTooManyRecords, off)
	}
}
