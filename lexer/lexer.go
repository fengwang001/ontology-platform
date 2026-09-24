// Package lexer 是逐字节、可暂停续传的 CSV(RFC4180 方言) 词法状态机。
// 每字节恰好处理一次、不回扫；只产出字段/记录事件，不做列数组装。
package lexer

import (
	"errors"
	"strconv"

	"ontology/cell"
)

// Limits 为解析上限，0 表示不限。
type Limits struct{ MaxFieldBytes int }

const ( // 状态
	StIdle byte = iota
	StStart
	StUnquoted
	StQuoted
	StQuoteSeen
	StCR
)

const ( // 事件种类
	EvCell = iota + 1
	EvEnd
	EvBlank
	EvError
)

// Event 是状态机事件；Term 为 EvEnd/EvBlank 的终止符字节偏移。
type Event struct {
	Kind int
	C    cell.Cell
	Term int
	Err  *Error
}

// 哨兵错误，彼此可区分。
var (
	ErrBadQuote          = errors.New("csv: unescaped '\"' in unquoted field")
	ErrCharAfterQuote    = errors.New("csv: unexpected char after closing quote")
	ErrUnterminatedQuote = errors.New("csv: unterminated quoted field")
	ErrLoneCR            = errors.New("csv: bare '\\r' not followed by '\\n'")
	ErrFieldTooLarge     = errors.New("csv: field exceeds MaxFieldBytes")
	ErrTerminal          = errors.New("csv: parser is in terminal state")
)

// Error 携带字节偏移（从 0 起）、记录号、字段号（从 1 起）。
type Error struct {
	Err           error
	Offset        int
	Record, Field int
	Cause         error
}

func (e *Error) Error() string {
	return e.Err.Error() + " at offset " + strconv.Itoa(e.Offset) +
		" (record " + strconv.Itoa(e.Record) + ", field " + strconv.Itoa(e.Field) + ")"
}

// Unwrap 让 errors.Is 同时识别终态包装与其根因。
func (e *Error) Unwrap() []error {
	if e.Cause != nil {
		return []error{e.Err, e.Cause}
	}
	return []error{e.Err}
}

// Snap 是状态机在某字节边界的快照，供并行段拼接使用。
type Snap struct {
	State                               byte
	Partial                             string
	Quoted, Closed, Dirty               bool
	Start, CloseEnd, CRPos, Off, CurLen int
}

// Machine 是增量状态机。单实例非并发安全。
type Machine struct {
	lim                                 int
	st                                  byte
	val                                 []byte
	quoted, closed, dirty               bool
	start, closeEnd, crPos, off, curLen int
	rec, fld, recsDone                  int
	bc                                  int64
	fatal                               *Error
	out                                 func(Event)
}

// NewMachine 创建流式状态机（全局坐标，记录/字段号从 1 起）。
func NewMachine(lim Limits, out func(Event)) *Machine {
	return &Machine{lim: lim.MaxFieldBytes, st: StIdle, out: out}
}

func (m *Machine) BytesProcessed() int64 { return m.bc }
func (m *Machine) Err() *Error           { return m.fatal }

func (m *Machine) fail(err error, pos int) *Error {
	if m.fatal == nil {
		m.fatal = &Error{Err: err, Offset: pos, Record: m.rec, Field: m.fld}
	}
	return m.fatal
}

func (m *Machine) emit(k int, c cell.Cell, t int) { m.out(Event{Kind: k, C: c, Term: t}) }

func (m *Machine) newRow(pos int, nf int) {
	m.rec, m.fld = m.recsDone+1, nf
	m.val, m.curLen = m.val[:0], 0
	m.quoted, m.closed, m.dirty = false, false, false
	m.start, m.st = pos, StStart
}
func (m *Machine) startRecord(pos int) { m.newRow(pos, 1) }
func (m *Machine) beginField(pos int)   { m.newRow(pos, m.fld+1) }

func (m *Machine) cell(end int) cell.Cell {
	return cell.Cell{Value: string(m.val), Quoted: m.quoted, Start: m.start, End: end}
}

func (m *Machine) add(b byte, pos int) *Error {
	if m.lim > 0 && m.curLen >= m.lim {
		return m.fail(ErrFieldTooLarge, pos)
	}
	m.val, m.curLen = append(m.val, b), m.curLen+1
	return nil
}

// Feed 喂入一段字节；遇致命错误立即返回并进入终态。
func (m *Machine) Feed(p []byte) *Error {
	if m.fatal != nil {
		return &Error{Err: ErrTerminal, Offset: m.off, Record: m.rec, Field: m.fld, Cause: m.fatal}
	}
	for _, b := range p {
		pos := m.off
		m.off, m.bc = pos+1, m.bc+1
		if m.st == StIdle {
			m.startRecord(pos)
		}
		if e := m.step(b, pos); e != nil {
			return e
		}
	}
	return nil
}

// step 处理单个字节（pos 为其全局偏移）。
func (m *Machine) step(b byte, pos int) *Error {
	switch m.st {
	case StStart, StUnquoted:
		switch {
		case b == ',':
			m.emit(EvCell, m.cell(pos), 0)
			if m.st == StStart {
				m.dirty = true
			}
			m.beginField(pos + 1)
		case b == '"':
			return m.fail(ErrBadQuote, pos)
		case b == '\n':
			if m.st == StStart && m.fld == 1 && m.curLen == 0 && !m.quoted {
				m.emit(EvBlank, cell.Cell{}, pos)
				m.st = StIdle
			} else {
				m.finishRow(pos)
			}
		case b == '\r':
			m.crPos, m.st = pos, StCR
		default:
			if e := m.add(b, pos); e != nil {
				return e
			}
			m.dirty, m.st = true, StUnquoted
		}
	case StQuoted:
		if b == '"' {
			m.closed, m.closeEnd, m.st = true, pos+1, StQuoteSeen
		} else if e := m.add(b, pos); e != nil {
			return e
		}
	case StQuoteSeen:
		switch {
		case b == '"':
			if e := m.add('"', pos); e != nil {
				return e
			}
			m.closed, m.st = false, StQuoted
		case b == ',' || b == '\n':
			if b == ',' {
				m.emit(EvCell, m.cell(m.closeEnd), 0)
				m.dirty = true
				m.beginField(pos + 1)
			} else {
				m.finishRow(pos)
			}
		case b == '\r':
			m.crPos, m.st = pos, StCR
		default:
			return m.fail(ErrCharAfterQuote, pos)
		}
	case StCR:
		if b != '\n' {
			return m.fail(ErrLoneCR, m.crPos)
		}
		m.finishCR()
	}
	return nil
}

func (m *Machine) finishRow(term int) {
	m.emit(EvCell, m.cell(term), 0)
	m.emit(EvEnd, cell.Cell{}, term)
	m.recsDone, m.st = m.rec, StIdle
}

func (m *Machine) finishCR() {
	if !m.dirty {
		m.emit(EvBlank, cell.Cell{}, m.crPos)
	} else {
		end := m.crPos
		if m.closed {
			end = m.closeEnd
		}
		m.emit(EvCell, m.cell(end), 0)
		m.emit(EvEnd, cell.Cell{}, m.crPos)
		m.recsDone = m.rec
	}
	m.st = StIdle
}

// Finish 处理流结束：无换行收尾、未闭合引号、悬垂 \r。
func (m *Machine) Finish() *Error {
	if m.fatal != nil {
		return &Error{Err: ErrTerminal, Offset: m.off, Record: m.rec, Field: m.fld, Cause: m.fatal}
	}
	switch m.st {
	case StQuoted:
		return m.fail(ErrUnterminatedQuote, m.off)
	case StCR:
		return m.fail(ErrLoneCR, m.crPos)
	case StIdle:
		return nil
	}
	if m.dirty {
		end := m.off
		if m.closed {
			end = m.closeEnd
		}
		m.emit(EvCell, m.cell(end), 0)
		m.emit(EvEnd, cell.Cell{}, m.off)
		m.recsDone = m.rec
	}
	return nil
}

// SegResult 是一个并行段在单一入口假设下的运行结果。
type SegResult struct {
	Events []Event
	Exit   Snap
	Count  int64
}

// RunSegment 从 base 偏移起解析 seg；inQuoted 表示段起点是否在引号字段内。
// 段内记录/字段号从 1 起，事件偏移为绝对偏移。
func RunSegment(lim Limits, seg []byte, base int, inQuoted bool) SegResult {
	m := &Machine{lim: lim.MaxFieldBytes, rec: 1, fld: 1, off: base}
	if inQuoted {
		m.st, m.quoted, m.dirty, m.start = StQuoted, true, true, base
	} else {
		m.st, m.start = StStart, base
	}
	r := SegResult{}
	m.out = func(ev Event) { r.Events = append(r.Events, ev) }
	if e := m.Feed(seg); e != nil {
		r.Events = append(r.Events, Event{Kind: EvError, Err: e})
	}
	r.Exit = Snap{State: m.st, Partial: string(m.val), Quoted: m.quoted, Closed: m.closed,
		Dirty: m.dirty, Start: m.start, CloseEnd: m.closeEnd, CRPos: m.crPos, Off: m.off, CurLen: m.curLen}
	r.Count = m.bc
	return r
}
