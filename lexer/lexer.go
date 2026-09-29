// Package lexer 是 RFC4180 方言 CSV 的逐字节状态机，可暂停续传。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 可判定的哨兵错误。
var (
	ErrBareQuote      = errors.New("bare quote in unquoted field")
	ErrQuoteGarbage   = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote  = errors.New("unclosed quoted field at EOF")
	ErrLoneCR         = errors.New("lone carriage return")
	ErrColumnMismatch = errors.New("record field count mismatch")
	ErrFieldTooLarge  = errors.New("field exceeds max bytes")
	ErrTooManyFields  = errors.New("record exceeds max fields")
	ErrTooManyRecords = errors.New("too many records")
	ErrTerminal       = errors.New("parser already in terminal state")
)

const ( // 状态
	sStart byte = iota
	sField
	sQuote
	sQQuote
	sCR
	sCRQ
)

// Event 是状态机吐出的一个事件。Err 非空时其余字段无意义。
type Event struct {
	Cell    cell.Cell
	RowEnd  bool
	Blank   bool // 0 字段的物理行（空行）
	Err     error
	Rec     int // 物理记录号（从 1）
	Fld     int // 字段号（从 1）
	Bytes   int // 本事件触发处的全局字节偏移
}

// Limits 为可配置上限；0 表示不限制。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// St 是一段状态机的可暂停快照（par 与流式共用）。
type St struct {
	State byte
	Val   []byte // 当前未闭合字段已累积的值
	QS, QE int   // 当前字段原文起止（QE 排他，含引号）
	Quoted bool
	Rec, Fld int
	Off      int // 已提交的全局字节基数
	Rows     int // 已闭合物理行数
	Steps    int // 字节被状态机处理的总次数（非导出计数器）
	lim      Limits
	enf      bool
	term     error
}

// New 建一个流式解析器快照。
func New(l Limits) *St { return &St{lim: l, enf: true} }

// Steps 暴露非导出计数器的读数。
func (s *St) StepsCount() int { return s.Steps }

// Terminal 返回终态错误（nil 表示尚未进入终态）。
func (s *St) Terminal() error { return s.term }

// PosError 给哨兵错误附加位置信息。
type PosError struct {
	Err        error
	ByteOffset int
	Record     int
	Field      int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d record %d field %d", e.Err, e.ByteOffset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

func (s *St) fail(ev *Event, err error, at int, fld int) {
	pe := &PosError{Err: err, ByteOffset: s.Off + at, Record: s.Rec, Field: fld}
	s.term = pe
	ev.Err = pe
}

// add 向当前字段追加一个逻辑字符并在第一超限字节处拒绝。
func (s *St) add(ev *Event, b byte, at int) bool {
	if s.enf && s.lim.MaxFieldBytes > 0 && len(s.Val) >= s.lim.MaxFieldBytes {
		s.fail(ev, ErrFieldTooLarge, at, s.Fld)
		return false
	}
	s.Val = append(s.Val, b)
	return true
}

func (s *St) emit(ev *Event, end int) {
	ev.Cell = cell.Cell{Value: append([]byte(nil), s.Val...), Quoted: s.Quoted, Start: s.QS + s.Off, End: end + s.Off}
	ev.Fld = s.Fld
	ev.Bytes = s.QS + s.Off
	s.Val, s.Quoted = nil, false
}

// Run 从给定初始状态跑 p；init 提供段起点快照（par 双假设用），流式用 s 自身。
// 返回事件序列（调用方 cap 预留）与跑完后的快照。
func Run(init St, p []byte, enforce bool) (St, []Event) {
	s := init
	evs := make([]Event, 0, len(p)/4+1)
	var ev Event
	for at := 0; at < len(p); at++ {
		s.Steps++
		b := p[at]
		ev = Event{Rec: s.Rec}
		switch s.State {
		case sStart:
			s.QS, s.QE = at, at
			switch b {
			case ',':
				s.emit(&ev, at)
				evs = append(evs, ev)
				s.Fld++
			case '"':
				s.Quoted, s.State = true, sQuote
			case '\n':
				ev.RowEnd, ev.Blank, ev.Rec, ev.Bytes = true, s.Fld == 0, s.Rec, s.Off+at
				evs = append(evs, ev)
				s.afterRow(at + 1)
			case '\r':
				s.State = sCR
			default:
				if !s.add(&ev, b, at) {
					return s.fin(evs, ev)
				}
				s.State = sField
			}
		case sField:
			switch b {
			case ',':
				s.emit(&ev, at)
				evs = append(evs, ev)
				s.Fld++
				s.State = sStart
			case '"':
				s.fail(&ev, ErrBareQuote, at, s.Fld)
				return s.fin(evs, ev)
			case '\n':
				s.emit(&ev, at)
				ev.RowEnd, ev.Rec, ev.Bytes = true, s.Rec, s.Off+at
				evs = append(evs, ev)
				s.afterRow(at + 1)
			case '\r':
				s.State = sCR
			default:
				if !s.add(&ev, b, at) {
					return s.fin(evs, ev)
				}
			}
		case sQuote:
			if b == '"' {
				s.State = sQQuote
			} else if !s.add(&ev, b, at) {
				return s.fin(evs, ev)
			}
		case sQQuote:
			switch b {
			case '"':
				if !s.add(&ev, '"', at) {
					return s.fin(evs, ev)
				}
				s.State = sQuote
			case ',':
				s.emit(&ev, at)
				evs = append(evs, ev)
				s.Fld++
				s.State = sStart
			case '\n':
				s.emit(&ev, at)
				ev.RowEnd, ev.Rec, ev.Bytes = true, s.Rec, s.Off+at
				evs = append(evs, ev)
				s.afterRow(at + 1)
			case '\r':
				s.State = sCRQ
			default:
				s.fail(&ev, ErrQuoteGarbage, at, s.Fld)
				return s.fin(evs, ev)
			}
		case sCR, sCRQ:
			if b == '\n' {
				if s.State == sCR {
					s.emit(&ev, at-1)
				} else {
					s.emit(&ev, at-1)
				}
				ev.RowEnd, ev.Rec, ev.Bytes = true, s.Rec, s.Off+at
				evs = append(evs, ev)
				s.afterRow(at + 1)
			} else {
				s.fail(&ev, ErrLoneCR, at-1, s.Fld)
				return s.fin(evs, ev)
			}
		}
		s.QE = at + 1
	}
	return s, evs
}

func (s *St) afterRow(nextAt int) {
	s.Rows++
	s.Rec++
	s.Fld = 0
	s.Val, s.Quoted = nil, false
	s.State = sStart
	s.QS, s.QE = nextAt, nextAt
}

func (s *St) fin(evs []Event, ev Event) (St, []Event) {
	if ev.Err != nil {
		evs = append(evs, ev)
	}
	return *s, evs
}

// Finish 在流结束时收口，返回残余事件（无尾换行记录或错误）。
func (s *St) Finish() []Event {
	ev := Event{Rec: s.Rec}
	switch s.State {
	case sStart:
		return nil
	case sQuote:
		s.fail(&ev, ErrUnclosedQuote, len(s.Val), s.Fld)
	case sField:
		s.emit(&ev, 0)
		ev.RowEnd, ev.Rec, ev.Bytes = true, s.Rec, s.QS
	case sQQuote:
		s.emit(&ev, 0)
		ev.RowEnd, ev.Rec, ev.Bytes = true, s.Rec, s.QS
	case sCR, sCRQ:
		s.fail(&ev, ErrLoneCR, -1, s.Fld)
	}
	if ev.Err != nil || ev.RowEnd {
		return []Event{ev}
	}
	return nil
}
