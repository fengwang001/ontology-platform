// Package lexer 是可暂停续传的 CSV 逐字节状态机。单个 Lexer 实例非并发安全。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 哨兵错误：四类语法错误 + 孤立 CR。
var (
	ErrBareQuote     = errors.New("bare double quote in unquoted field")
	ErrAfterQuote    = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote = errors.New("unclosed quoted field at end of input")
	ErrBareCR        = errors.New("bare carriage return not followed by newline")
)

// 上限错误。
var (
	ErrFieldTooLong   = errors.New("field exceeds max byte length")
	ErrTooManyFields  = errors.New("record exceeds max field count")
	ErrTooManyRecords = errors.New("input exceeds max record count")
)

// State 是状态机当前状态，供 par 分段复用。
type State int

const (
	SStart State = iota // 字段开始
	SUnq                // 未引号字段中
	SQ                  // 引号字段中
	SQA                 // 引号字段中刚见一个引号
	SCR                 // 行尾 CR 待定（closed 区分引号/未引号来源）
)

// Limits 为 0 表示不限。
type Limits struct {
	MaxFieldBytes      int
	MaxFieldsPerRecord int
	MaxRecords         int
}

// ParseError 携带全局字节偏移（从 0）、记录号、字段号（从 1）。
type ParseError struct {
	Err    error
	Off    int
	Record int
	Field  int
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%v at byte %d record %d field %d", e.Err, e.Off, e.Record, e.Field)
}
func (e *ParseError) Unwrap() error { return e.Err }

// Handler 接收词法事件；OnField 在一个字段确定时回调，OnRecord 在非空记录结束时回调。
// OnRecord 返回非 nil 时该错误终止状态机（用于上层列数校验）。
type Handler interface {
	OnField(cell.Cell)
	OnRecord() error
}

type machine struct {
	h    Handler
	lim  Limits
	st   State
	pos  int
	// 当前活动字段
	start                       int
	quoted, started, recStarted bool
	closed                      bool
	closeEnd, crOff             int
	val                         []byte
	flen, fields, records, n    int
	terminal                    *ParseError
}

func newMachine(h Handler, lim Limits) *machine { return &machine{h: h, lim: lim} }

func (m *machine) fail(err error, off int) *ParseError {
	if m.terminal == nil {
		m.terminal = &ParseError{Err: err, Off: off, Record: m.records + 1, Field: m.fields + 1}
	}
	return m.terminal
}

// begin 开始一个字段（字段计数在这里发生）。
func (m *machine) begin(off int, quoted bool) *ParseError {
	if m.lim.MaxFieldsPerRecord > 0 && m.fields >= m.lim.MaxFieldsPerRecord {
		return m.fail(ErrTooManyFields, off)
	}
	m.fields++
	m.start, m.quoted, m.started = off, quoted, true
	m.closed = false
	m.val, m.flen = m.val[:0], 0
	return nil
}

func (m *machine) add(b byte, off int) *ParseError {
	m.flen++
	if m.lim.MaxFieldBytes > 0 && m.flen > m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooLong, off)
	}
	m.val = append(m.val, b)
	return nil
}

func (m *machine) emit(end int) {
	m.h.OnField(cell.Cell{Value: string(m.val), Quoted: m.quoted, Start: m.start, End: end})
	m.started = false
}

func (m *machine) commit(off int) *ParseError {
	if !m.recStarted {
		m.st = SStart
		return nil // 空记录抑制
	}
	m.records++
	if m.lim.MaxRecords > 0 && m.records > m.lim.MaxRecords {
		return m.fail(ErrTooManyRecords, off)
	}
	if e := m.h.OnRecord(); e != nil {
		return m.fail(e, off)
	}
	m.fields, m.recStarted, m.st = 0, false, SStart
	return nil
}

// finishOpen 关闭活动字段：lineEnd 为行尾首字节偏移；quoted 字段 end 用 closeEnd。
func (m *machine) finishOpen(lineEnd int) {
	if !m.started {
		return
	}
	if m.quoted && m.closed {
		m.emit(m.closeEnd)
	} else {
		m.emit(lineEnd)
	}
}

// feed 处理一段字节。
func (m *machine) feed(p []byte, base int) *ParseError {
	for i, b := range p {
		if m.terminal != nil {
			return m.terminal
		}
		off := base + i
		m.n++
		switch m.st {
		case SStart:
			switch b {
			case ',':
				if e := m.begin(off, false); e != nil {
					return e
				}
				m.emit(off)
				m.recStarted = true
			case '"':
				if e := m.begin(off, true); e != nil {
					return e
				}
				m.recStarted = true
				m.st = SQ
			case '\r':
				m.crOff = off
				m.st = SCR
			case '\n':
				if e := m.commit(off); e != nil {
					return e
				}
			default:
				if e := m.begin(off, false); e != nil {
					return e
				}
				if e := m.add(b, off); e != nil {
					return e
				}
				m.recStarted = true
				m.st = SUnq
			}
		case SUnq:
			switch b {
			case ',':
				m.emit(off)
				m.st = SStart
			case '"':
				return m.fail(ErrBareQuote, off)
			case '\r':
				m.crOff = off
				m.st = SCR
			case '\n':
				m.emit(off)
				if e := m.commit(off); e != nil {
					return e
				}
			default:
				if e := m.add(b, off); e != nil {
					return e
				}
			}
		case SQ:
			if b == '"' {
				m.closed, m.closeEnd, m.st = true, off+1, SQA
			} else if e := m.add(b, off); e != nil {
				return e
			}
		case SQA:
			switch b {
			case ',':
				m.emit(m.closeEnd)
				m.st = SStart
			case '"':
				m.closed = false
				if e := m.add('"', off); e != nil {
					return e
				}
				m.st = SQ
			case '\r':
				m.crOff = off
				m.st = SCR
			case '\n':
				m.emit(m.closeEnd)
				if e := m.commit(off); e != nil {
					return e
				}
			default:
				return m.fail(ErrAfterQuote, off)
			}
		case SCR:
			if b != '\n' {
				return m.fail(ErrBareCR, m.crOff)
			}
			m.finishOpen(m.crOff)
			if e := m.commit(off); e != nil {
				return e
			}
		}
		m.pos = off + 1
	}
	return m.terminal
}

// closeAt 在流结束时收尾，endOff 为总字节数。
func (m *machine) closeAt(endOff int) *ParseError {
	if m.terminal != nil {
		return m.terminal
	}
	switch m.st {
	case SQ:
		return m.fail(ErrUnclosedQuote, m.start)
	case SCR:
		return m.fail(ErrBareCR, m.crOff)
	case SUnq:
		m.emit(endOff)
		_ = m.commit(endOff)
	case SQA:
		m.emit(m.closeEnd)
		_ = m.commit(endOff)
	}
	return m.terminal
}

// Lexer 是流式解析器：Feed 可调用任意多次，Close 收尾；出错后进入终态。
type Lexer struct{ m machine }

func New(h Handler, lim Limits) *Lexer { return &Lexer{m: *newMachine(h, lim)} }

func (l *Lexer) Feed(p []byte) error {
	if e := l.m.feed(p, l.m.pos); e != nil {
		return e
	}
	return nil
}

func (l *Lexer) Close() error { return l.m.closeAt(l.m.pos) }

// Bytes 返回状态机处理过的字节总数。
func (l *Lexer) Bytes() int { return l.m.n }

type collector struct {
	cells []cell.Cell
	recs  [][]cell.Cell
}

func (c *collector) OnField(x cell.Cell) { c.cells = append(c.cells, x) }
func (c *collector) OnRecord() error {
	c.recs = append(c.recs, c.cells)
	c.cells = nil
	return nil
}

// RunSegment 从 start 状态开始解析 data（首字节全局偏移 base），不做 EOF 收尾，
// 供 par 双假设使用。Cells 为段内已确定字段（含段末未闭合字段），
// Records 为段内已结束记录的下标区间 [0,len(recs))。
func RunSegment(data []byte, base int, start State, lim Limits) *SegResult {
	c := &collector{}
	m := newMachine(c, lim)
	m.st, m.pos = start, base
	if start == SQ {
		if e := m.begin(base, true); e != nil {
			return &SegResult{Err: e, EndState: start}
		}
		m.recStarted = true
	}
	err := m.feed(data, base)
	r := &SegResult{EndState: m.st, Err: err, N: m.n,
		Records: m.records, Fields: len(c.cells)}
	r.Cells = c.cells
	if m.started {
		x := cell.Cell{Value: string(m.val), Quoted: m.quoted, Start: m.start}
		if m.quoted && m.closed {
			x.End = m.closeEnd
		} else {
			x.End = base + len(data)
		}
		r.Cells = append(r.Cells, x)
		r.Open = true
	}
	return r
}

// SegResult 是一段缓冲区在给定起始状态下的解析结果。
type SegResult struct {
	Cells    []cell.Cell
	Records  int // 段内提交的完整记录数（空记录已抑制）
	Fields   int // 完整记录中的字段总数（不含 Open）
	Open     bool
	EndState State
	Err      *ParseError
	N        int
}
