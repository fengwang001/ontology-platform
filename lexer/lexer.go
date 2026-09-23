// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误与两类字段上限，彼此可用 errors.Is 判定。
var (
	ErrBareQuote      = errors.New("bare quote in unquoted field")
	ErrCharAfterQuote = errors.New("unexpected byte after closing quote")
	ErrUnterminated   = errors.New("unterminated quoted field at EOF")
	ErrBareCR         = errors.New("bare CR not followed by LF")
	ErrFieldTooLong   = errors.New("field exceeds max bytes")
	ErrTooManyFields  = errors.New("record exceeds max field count")
	ErrClosed         = errors.New("feed after parser closed")
)

// Error 携带字节偏移（从 0 起）、记录号、字段号（从 1 起）。
type Error struct {
	Kind          error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v: offset=%d record=%d field=%d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// Limits 为资源上限，0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

// State 是状态机在一段边界处的状态。
type State int

const (
	FStart     State = iota // 字段开始（记录内尚无字段产出）
	InField                 // 未引号字段中
	InQuote                 // 引号字段中
	QuoteSeen               // 引号字段中刚见一个引号
	CrDangling              // 见 \r，悬置字段尚未闭合
	CrClosed                // 见 \r，字段已闭合
	CrBlank                 // 记录起始处见 \r，可能是空行
)

func openState(s State) bool { return s == InField || s == InQuote || s == QuoteSeen }

// Event 是一个完整字段；RecEnd 表示它同时闭合一条记录。
type Event struct {
	C       cell.Cell
	RecEnd  bool
	Rec, Fl int
	Carried bool // 字段在本段之前已开始（供 par 拼接）
}

// Tail 是一段解析结束时的悬置状态。
type Tail struct {
	State        State
	Cont         bool // 字段跨段未闭合
	Partial      string
	Quoted       bool
	FStart       int
	CrOff        int
	Rec, Emitted int // 起始记录号(1起)；当前记录已闭合字段数
}

// Result 是一段字节的解析结果。
type Result struct {
	Events []Event
	Err    *Error
	Tail   Tail
	NBytes int
}

type machine struct {
	st        State
	buf       []byte
	quoted    bool
	fstart    int
	rec, em   int
	carryOpen bool
	n         int
	lim       Limits
	croff     int
}

func (m *machine) fail(k error, off int) *Error {
	return &Error{Kind: k, Offset: off, Record: m.rec, Field: m.em + 1}
}

func (m *machine) add(b byte, off int) *Error {
	if m.lim.MaxFieldBytes > 0 && len(m.buf) >= m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooLong, off)
	}
	m.buf = append(m.buf, b)
	return nil
}

func (m *machine) closeField(off int, recEnd bool) Event {
	m.em++
	e := Event{
		C:      cell.Cell{Value: string(m.buf), Quoted: m.quoted, Start: m.fstart, End: off},
		RecEnd: recEnd, Rec: m.rec, Fl: m.em, Carried: m.carryOpen,
	}
	m.buf = nil
	m.quoted = false
	return e
}

func (m *machine) beginRec(off int) {
	m.rec++
	m.em = 0
	m.st = FStart
	m.fstart = off
	m.carryOpen = false
}

func (m *machine) checkFields(off int) *Error {
	if m.lim.MaxFields > 0 && m.em+1 > m.lim.MaxFields {
		return m.fail(ErrTooManyFields, off)
	}
	return nil
}

// Run 在给定边界状态下解析 p（绝对起点 base），纯函数式，可并行复用。
func Run(p []byte, base int, st State, rec, emitted int, fstart int, quoted bool, lim Limits) Result {
	m := machine{st: st, rec: rec, em: emitted, fstart: fstart, quoted: quoted, lim: lim}
	m.carryOpen = openState(st)
	if !m.carryOpen {
		m.fstart = base
	}
	var ev []Event
	emit := func(e Event) { ev = append(ev, e) }
	for i := 0; i < len(p); i++ {
		off := base + i
		b := p[i]
		m.n++
		switch m.st {
		case FStart:
			switch b {
			case ',':
				if e := m.checkFields(off); e != nil {
					return m.fin(ev, e)
				}
				emit(m.closeField(off, false))
				m.st, m.fstart = FStart, off+1
			case '\n':
				if m.em > 0 {
					emit(m.closeField(off, true))
					m.beginRec(off + 1)
				} else {
					m.fstart = off + 1 // 空行跳过
				}
			case '\r':
				m.st, m.croff = CrBlank, off
			case '"':
				m.st, m.quoted, m.fstart = InQuote, true, off
			default:
				if e := m.add(b, off); e != nil {
					return m.fin(ev, e)
				}
				m.st = InField
			}
		case InField:
			switch b {
			case ',':
				if e := m.checkFields(off); e != nil {
					return m.fin(ev, e)
				}
				emit(m.closeField(off, false))
				m.st, m.fstart = FStart, off+1
			case '\n':
				emit(m.closeField(off, true))
				m.beginRec(off + 1)
			case '\r':
				m.st, m.croff = CrDangling, off
			case '"':
				return m.fin(ev, m.fail(ErrBareQuote, off))
			default:
				if e := m.add(b, off); e != nil {
					return m.fin(ev, e)
				}
			}
		case InQuote:
			if b == '"' {
				m.st = QuoteSeen
			} else if e := m.add(b, off); e != nil {
				return m.fin(ev, e)
			}
		case QuoteSeen:
			switch b {
			case '"':
				if e := m.add('"', off); e != nil {
					return m.fin(ev, e)
				}
				m.st = InQuote
			case ',':
				if e := m.checkFields(off); e != nil {
					return m.fin(ev, e)
				}
				emit(m.closeField(off, false))
				m.st, m.fstart = FStart, off+1
			case '\n':
				emit(m.closeField(off, true))
				m.beginRec(off + 1)
			case '\r':
				m.st, m.croff = CrClosed, off
			default:
				return m.fin(ev, m.fail(ErrCharAfterQuote, off))
			}
		case CrDangling, CrClosed, CrBlank:
			if b != '\n' {
				return m.fin(ev, m.fail(ErrBareCR, m.croff))
			}
			if m.st == CrDangling {
				emit(m.closeField(off, true))
			}
			m.beginRec(off + 1)
		}
	}
	return m.fin(ev, nil)
}

func (m *machine) fin(ev []Event, err *Error) Result {
	t := Tail{State: m.st, Rec: m.rec, Emitted: m.em, Quoted: m.quoted, FStart: m.fstart, CrOff: m.croff}
	if openState(m.st) {
		t.Cont = true
		t.Partial = string(m.buf)
	}
	return Result{Events: ev, Err: err, Tail: t, NBytes: m.n}
}

// Finalize 处理流结束：end 为流总字节数。返回末字段事件与/或终态错误。
func Finalize(t Tail, end int) (ev []Event, err *Error) {
	switch t.State {
	case InQuote:
		return nil, &Error{Kind: ErrUnterminated, Offset: t.FStart, Record: t.Rec, Field: t.Emitted + 1}
	case CrDangling, CrClosed, CrBlank:
		return nil, &Error{Kind: ErrBareCR, Offset: t.CrOff, Record: t.Rec, Field: t.Emitted + 1}
	case InField, FStart, QuoteSeen:
		if t.Emitted > 0 || t.State == InField || t.State == QuoteSeen {
			fl := t.Emitted + 1
			return []Event{{C: cell.Cell{Value: t.Partial, Quoted: t.Quoted,
				Start: t.FStart, End: end}, RecEnd: true, Rec: t.Rec, Fl: fl}}, nil
		}
	}
	return nil, nil
}

// Lexer 是单线程、可半包续传的流式解析器；非并发安全。
type Lexer struct {
	lim    Limits
	t      Tail
	pos    int
	ev     []Event
	err    *Error
	closed bool
	n      int
}

func New(lim Limits) *Lexer { return &Lexer{lim: lim, t: Tail{State: FStart, Rec: 1}} }

// Feed 送入一段字节。
func (l *Lexer) Feed(p []byte) error {
	if l.err != nil {
		return l.err
	}
	if l.closed {
		l.err = &Error{Kind: ErrClosed, Offset: l.pos}
		return l.err
	}
	r := Run(p, l.pos, l.t.State, l.t.Rec, l.t.Emitted, l.t.FStart, l.t.Quoted, l.lim)
	l.n += r.NBytes
	l.pos += len(p)
	l.ev = append(l.ev, r.Events...)
	l.t = r.Tail
	if r.Err != nil {
		l.err = r.Err
		return l.err
	}
	return nil
}

// Close 结束流并取出残留事件。
func (l *Lexer) Close() error {
	if l.err != nil {
		return l.err
	}
	if l.closed {
		return nil
	}
	l.closed = true
	ev, err := Finalize(l.t, l.pos)
	l.ev = append(l.ev, ev...)
	l.err = err
	return err
}

// Events 返回截至当前已产出的完整字段事件。
func (l *Lexer) Events() []Event { return l.ev }

// NBytes 返回状态机处理过的字节总数。
func (l *Lexer) NBytes() int { return l.n }

// Err 返回终态错误（无则 nil）。
func (l *Lexer) Err() *Error { return l.err }
