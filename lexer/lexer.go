// Package lexer 是可半包续传的 CSV 逐字节状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误与两类词法上限（记录数上限在 table 层）。
var (
	ErrQuoteInBare    = errors.New("bare field contains quote")
	ErrJunkAfterQuote = errors.New("unexpected char after closing quote")
	ErrUnterminated   = errors.New("unterminated quoted field")
	ErrBareCR         = errors.New("bare carriage return not followed by LF")
	ErrFieldTooLarge  = errors.New("field exceeds max bytes")
	ErrTooManyFields  = errors.New("record exceeds max fields")
)

// Limits 是词法层可配置上限，0 表示不限。
type Limits struct{ MaxFieldBytes, MaxFieldsPerRecord int }

// Error 携带字节偏移(0起)、记录号、字段号(1起)，可用 errors.Is 判别类别。
type Error struct {
	Kind           error
	Offset, Record int
	Field          int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d field %d)", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// Event：EndRecord=true 时 Offset 为行尾偏移；否则 C 为一个完整字段。
type Event struct {
	C         cell.Cell
	EndRecord bool
	Offset    int
	Slot      int
	Comma     bool // 字段由逗号闭合（其后有新槽位）
	Src       []int
}

const (
	stField = iota
	stBare
	stQ
	stQQ
	stCR
	stQCR
)

type machine struct {
	h                                  func(Event)
	maxF, maxFields                    int
	steps                              int64
	state                              int
	has, quoted                        bool
	buf                                []byte
	start, crOff, slot, recN, recs     int
	err                                *Error
	track                              bool
	src                                []int
}

func (m *machine) fail(kind error, off int) {
	if m.err == nil {
		m.err = &Error{Kind: kind, Offset: off, Record: m.recs + 1, Field: m.slot}
	}
}

func (m *machine) startField(off int) bool {
	if m.has {
		return true
	}
	m.slot = m.recN + 1
	if m.maxFields > 0 && m.slot > m.maxFields {
		m.fail(ErrTooManyFields, off)
		return false
	}
	m.has, m.quoted, m.start = true, false, off
	m.buf = m.buf[:0]
	return true
}

func (m *machine) emit(off int) {
	c := cell.Cell{Value: string(m.buf), Quoted: m.quoted, Start: m.start, End: off}
	ev := Event{C: c, Slot: m.slot}
	if m.track {
		ev.Src = m.src
		m.src = nil
	}
	m.h(ev)
	m.recN++
	m.has = false
}

func (m *machine) endRecord(off int) {
	m.h(Event{EndRecord: true, Offset: off})
	m.recs++
	m.recN, m.state = 0, stField
}

func (m *machine) add(b byte, off int) bool {
	if m.maxF > 0 && len(m.buf) >= m.maxF {
		m.fail(ErrFieldTooLarge, off)
		return false
	}
	m.buf = append(m.buf, b)
	if m.track && len(m.src) <= m.maxF {
		m.src = append(m.src, off)
	}
	return true
}

func (m *machine) step(b byte, off int) bool {
	m.steps++
	switch m.state {
	case stField:
		switch {
		case b == ',':
			if !m.startField(off) {
				return false
			}
			m.emit(off)
			m.state = stField
		case b == '"':
			if !m.startField(off) {
				return false
			}
			m.quoted = true
			m.state = stQ
		case b == '\r':
			if !m.startField(off) {
				return false
			}
			m.crOff, m.state = off, stCR
		case b == '\n':
			if m.recN == 0 {
				return true // 空行跳过
			}
			m.startField(off)
			m.emit(off)
			m.endRecord(off)
		default:
			if !m.startField(off) || !m.add(b, off) {
				return false
			}
			m.state = stBare
		}
	case stBare:
		switch {
		case b == ',':
			m.emit(off)
			m.state = stField
		case b == '"':
			m.fail(ErrQuoteInBare, off)
			return false
		case b == '\r':
			m.crOff, m.state = off, stCR
		case b == '\n':
			m.emit(off)
			m.endRecord(off)
		default:
			if !m.add(b, off) {
				return false
			}
		}
	case stQ:
		if b == '"' {
			m.state = stQQ
		} else if !m.add(b, off) {
			return false
		}
	case stQQ:
		switch {
		case b == '"':
			if !m.add('"', off) {
				return false
			}
			m.state = stQ
		case b == ',':
			m.emit(off)
			m.state = stField
		case b == '\r':
			m.crOff, m.state = off, stQCR
		case b == '\n':
			m.emit(off)
			m.endRecord(off)
		default:
			m.fail(ErrJunkAfterQuote, off)
			return false
		}
	case stCR, stQCR:
		quoted := m.state == stQCR
		if b == '\n' {
			m.emit(m.crOff)
			m.endRecord(m.crOff)
		} else {
			_ = quoted
			m.fail(ErrBareCR, m.crOff)
			return false
		}
	}
	return true
}

func (m *machine) finish(eof int) error {
	switch m.state {
	case stQ:
		m.fail(ErrUnterminated, m.start)
	case stCR, stQCR:
		m.fail(ErrBareCR, m.crOff)
	case stBare, stQQ:
		m.emit(eof)
		m.endRecord(eof)
	case stField:
		if m.recN > 0 {
			m.startField(eof)
			m.emit(eof)
			m.endRecord(eof)
		}
	}
	return m.err
}

// Lexer 是单线程流式解析器；单实例非并发安全。
type Lexer struct {
	m    machine
	term error
}

// New 创建流式解析器，h 依次收到字段与行尾事件。
func New(h func(Event), lim Limits) *Lexer {
	return &Lexer{m: machine{h: h, maxF: lim.MaxFieldBytes, maxFields: lim.MaxFieldsPerRecord, buf: make([]byte, 0, 64)}}
}

// Feed 喂入任意长度的字节块，可反复调用；进入终态后返回同一错误。
func (l *Lexer) Feed(p []byte) error {
	if l.term != nil {
		return l.term
	}
	base := l.m.steps // steps 同时也是已消费字节数
	for i, b := range p {
		if !l.m.step(b, base+i) {
			l.term = l.m.err
			return l.term
		}
	}
	return nil
}

// Close 结束流，处理无换行结尾与 EOF 处错误。
func (l *Lexer) Close() error {
	if l.term != nil {
		return l.term
	}
	l.term = l.m.finish(int(l.m.steps))
	return l.term
}

// Steps 返回状态机处理过的字节总数（流式下应等于输入字节数）。
func (l *Lexer) Steps() int64 { return l.m.steps }
