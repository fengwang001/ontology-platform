// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"strings"

	"ontology/cell"
)

var (
	ErrBareQuote     = errors.New("bare quote in unquoted field")
	ErrQuoteGarbage  = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote = errors.New("unterminated quoted field")
	ErrLoneCR        = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLarge = errors.New("field exceeds max bytes")
)

// Event 由状态机产出：一个字段结束；End 表示其后记录也结束。
type Event struct {
	Cell cell.Cell
	End  bool
}

// Sink 接收词法事件。
type Sink interface {
	Emit(ev Event) error
}

// Entry 是段级解析的入口假设。
type Entry int

const (
	EntryOutside Entry = iota // 段首在引号外、新字段槽开始
	EntryBare                 // 段首延续一个未引号字段
	EntryInQuote              // 段首在引号内，prefix 是重叠的边界字节
)

// SegResult 是一段在某入口假设下的结果（坐标均为全局字节偏移）。
type SegResult struct {
	Events  []Event
	Err     *PosError
	Open    *cell.Cell
	OpenVal string
	State   string // none|bare|quote|qseen|cr
	OpenSlot bool   // 段末有一个已开字段槽（可能为空）
}

// PosError 带字节偏移、记录号、字段号（从 1 起，偏移从 0 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

type state byte

const (
	stS0 state = iota
	stBARE
	stQ
	stQS
	stCR
)

type Lexer struct {
	sink      Sink
	maxFB     int
	processed int
	off       int
	recNo     int
	fldNo     int
	st        state
	val       strings.Builder
	start     int
	quoted    bool
	slot      bool
	seg       bool
	events    []Event
	segErr    *PosError
}

// New 创建流式状态机。maxFieldBytes 为 0 表示不限。
func New(sink Sink, maxFieldBytes int) *Lexer {
	return &Lexer{sink: sink, maxFB: maxFieldBytes}
}

func (l *Lexer) fail(err error, off int) error {
	return &PosError{Err: err, Offset: off, Record: l.recNo + 1, Field: l.fldNo + 1}
}

func (l *Lexer) emit(end bool, off int) error {
	c := cell.Cell{Value: l.val.String(), Quoted: l.quoted, Start: l.start, End: off}
	if l.seg {
		l.events = append(l.events, Event{Cell: c, End: end})
	} else if err := l.sink.Emit(Event{Cell: c, End: end}); err != nil {
		return err
	}
	l.val.Reset()
	l.quoted = false
	l.slot = false
	l.fldNo++
	if end {
		l.recNo++
		l.fldNo = 0
	}
	l.st = stS0
	return nil
}

func (l *Lexer) checkLen(off int) error {
	if l.maxFB > 0 && l.val.Len() > l.maxFB {
		return l.fail(ErrFieldTooLarge, off)
	}
	return nil
}

// Feed 喂入一段字节，可调用任意多次。
func (l *Lexer) Feed(p []byte) error {
	l.processed += len(p)
	for _, b := range p {
		if err := l.step(b, l.off); err != nil {
			return err
		}
		l.off++
	}
	return nil
}

func (l *Lexer) step(b byte, off int) error {
	switch l.st {
	case stS0, stBARE:
		switch b {
		case ',':
			return l.emit(false, off)
		case '\n':
			return l.emit(true, off+1)
		case '\r':
			l.slot = true
			l.val.WriteByte(b)
			l.st = stCR
		case '"':
			if l.st == stBARE {
				return l.fail(ErrBareQuote, off)
			}
			l.slot = true
			l.start = off
			l.quoted = true
			l.st = stQ
		default:
			if !l.slot {
				l.slot = true
				l.start = off
			}
			l.val.WriteByte(b)
			l.st = stBARE
			if err := l.checkLen(off); err != nil {
				return err
			}
		}
	case stQ:
		if b == '"' {
			l.st = stQS
		} else {
			l.val.WriteByte(b)
			if err := l.checkLen(off); err != nil {
				return err
			}
		}
	case stQS:
		switch b {
		case ',':
			return l.emit(false, off+1)
		case '\n':
			return l.emit(true, off+1)
		case '\r':
			l.val.WriteByte(b)
			l.st = stCR
		case '"':
			l.val.WriteByte(b)
			l.st = stQ
			if err := l.checkLen(off); err != nil {
				return err
			}
		default:
			return l.fail(ErrQuoteGarbage, off)
		}
	case stCR:
		if b == '\n' {
			v := l.val.String()
			l.val.Reset()
			l.val.WriteString(v[:len(v)-1])
			return l.emit(true, off+1)
		}
		return l.fail(ErrLoneCR, off-1)
	}
	return nil
}

// Close 表示流结束。
func (l *Lexer) Close() error {
	if l.seg {
		return nil
	}
	switch l.st {
	case stQ:
		return l.fail(ErrUnclosedQuote, l.off)
	case stCR:
		return l.fail(ErrLoneCR, l.off-1)
	}
	if l.slot {
		return l.emit(true, l.off)
	}
	return nil
}

// BytesProcessed 返回状态机处理过的字节总数。
func (l *Lexer) BytesProcessed() int { return l.processed }

// RunSegment 在某入口假设下解析 p（坐标 base 起）。EntryInQuote 时 p[0] 是
// 与左邻段重叠的边界字节，其动作只用于确定引号子态，不产生字段事件。
func RunSegment(p []byte, entry Entry, base int) *SegResult {
	l := &Lexer{seg: true, off: base}
	switch entry {
	case EntryBare:
		l.st, l.slot = stBARE, true
	case EntryInQuote:
		l.st, l.slot, l.quoted = stQ, true, true
	}
	l.processed += len(p)
	for i, b := range p {
		if entry == EntryInQuote && i == 0 {
			// 重叠字节：只推进引号子态，不进值。
			if b == '"' {
				l.st = stQS
			}
			l.off++
			continue
		}
		if err := l.step(b, l.off); err != nil {
			l.segErr = err.(*PosError)
			break
		}
		l.off++
	}
	return l.result()
}

func (l *Lexer) result() *SegResult {
	r := &SegResult{Events: l.events, Err: l.segErr, OpenSlot: l.slot}
	switch l.st {
	case stBARE:
		r.State = "bare"
	case stQ:
		r.State = "quote"
	case stQS:
		r.State = "qseen"
	case stCR:
		r.State = "cr"
	default:
		r.State = "none"
	}
	if l.slot {
		c := cell.Cell{Quoted: l.quoted, Start: l.start, End: l.off}
		r.Open = &c
		r.OpenVal = l.val.String()
	}
	return r
}
