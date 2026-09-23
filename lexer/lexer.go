// Package lexer 是可暂停续传的 RFC4180 方言逐字节状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 四类语法错误与三类上限错误，彼此可用 errors.Is 判定。
var (
	ErrBareQuote       = errors.New("bare quote in unquoted field")
	ErrQuoteAfterField = errors.New("unexpected char after closing quote")
	ErrUnterminated    = errors.New("unterminated quoted field")
	ErrBadCR           = errors.New("lone carriage return")
	ErrColumnCount     = errors.New("record field count mismatch")
	ErrFieldTooLarge   = errors.New("field exceeds max bytes")
	ErrTooManyFields   = errors.New("record exceeds max fields")
	ErrTooManyRecords  = errors.New("too many records")
)

// Error 携带字节偏移（从 0 起）与记录号、字段号（从 1 起）。
type Error struct {
	Kind          error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string {
	return e.Kind.Error()
}
func (e *Error) Unwrap() error { return e.Kind }

// Config 中上限为 0 表示不限。
type Config struct {
	MaxFieldBytes, MaxFields, MaxRecords int
}

// Sink 接收已完成字段与记录事件。EndRecord 在字段事件之后调用。
type Sink interface {
	Emit(cell.Cell)
	EndRecord() error
}

// Mode 是段末状态，供 par 拼接判定。
type Mode int

const (
	ModeFieldStart Mode = iota
	ModeUnquoted
	ModeQuoted
	ModeQuoteSeen
	ModeCR
)

// Ev 是段内事件：要么一个字段，要么一条记录结束（EndOff 为换行偏移）。
type Ev struct {
	Cell   cell.Cell
	End    bool
	EndOff int
}

// Seed 是段的起始假设；InQuote 为「起点已在引号字段内」。
type Seed struct {
	Off, RecNo, FldNo int
	InQuote           bool
	Unquoted          bool
}

// Result 是一段的双假设跑法之一的产出与末状态。
type Result struct {
	Events              []Ev
	Err                 *Error
	Mode                Mode
	RecNo, FldNo, FLen  int
	RowsDone, FieldsDone int
	Active, RecStarted  bool
	OpenBuf             []byte
	OpenStart           int
	Ops                 int64
}

type machine struct {
	cfg                                   Config
	sink                                  Sink
	mode                                  Mode
	off                                   int
	recNo, fldNo                          int
	rowsDone, fieldsDone                  int
	recStarted, quoted, active            bool
	fstart, flen, crOff                   int
	buf                                   []byte
	terminal                              *Error
	ops                                   int64
}

func newMachine(cfg Config, sink Sink, s Seed) *machine {
	recNo, fldNo := s.RecNo, s.FldNo
	if recNo == 0 {
		recNo = 1
	}
	if fldNo == 0 {
		fldNo = 1
	}
	m := &machine{cfg: cfg, sink: sink, off: s.Off, recNo: recNo, fldNo: fldNo,
		fstart: s.Off}
	if s.InQuote {
		m.mode, m.active, m.quoted, m.recStarted = ModeQuoted, true, true, true
	}
	if s.Unquoted {
		m.mode, m.active, m.recStarted = ModeUnquoted, true, true
	}
	return m
}

func (m *machine) fail(k error) *Error {
	if m.terminal == nil {
		m.terminal = &Error{Kind: k, Offset: m.off, Record: m.recNo, Field: m.fldNo}
	}
	return m.terminal
}

func (m *machine) add(b byte) *Error {
	if m.cfg.MaxFieldBytes > 0 && m.flen >= m.cfg.MaxFieldBytes {
		return m.fail(ErrFieldTooLarge)
}
	m.flen++
	m.buf = append(m.buf, b)
	return nil
}

func (m *machine) emit(end int) {
	m.sink.Emit(cell.Cell{Value: string(m.buf), Quoted: m.quoted, Start: m.fstart, End: end})
	m.buf, m.active, m.quoted, m.flen = m.buf[:0], false, false, 0
}

func (m *machine) comma() *Error {
	m.emit(m.off)
	if m.cfg.MaxFields > 0 && m.fldNo >= m.cfg.MaxFields {
		return m.fail(ErrTooManyFields)
	}
	m.fldNo++
	m.fstart, m.mode, m.recStarted = m.off+1, ModeFieldStart, true
	return nil
}

type endOffKey struct{}

func (m *machine) rowEnd(nlOff int) *Error {
	if e := m.sink.EndRecord(); e != nil {
		if er, ok := e.(*Error); ok {
			m.terminal = er
			return er
		}
		return m.fail(e)
	}
	if m.cfg.MaxRecords > 0 && m.recNo >= m.cfg.MaxRecords {
		return m.fail(ErrTooManyRecords)
	}
	m.rowsDone++
	m.fieldsDone = m.fldNo
	m.recNo++
	m.fldNo, m.recStarted, m.fstart = 1, false, m.off+1
	m.mode = ModeFieldStart
	return nil
}

func (m *machine) feed(data []byte) *Error {
	if m.terminal != nil {
		return m.terminal
	}
	for _, b := range data {
		m.ops++
		m.off++
		switch m.mode {
		case ModeFieldStart:
			switch b {
			case ',':
				if e := m.comma(); e != nil { return e }
			case '\n':
				if m.recStarted {
			m.emit(m.off - 1)
					if e := m.rowEnd(m.off-1); e != nil { return e }
				} else {
					m.fstart = m.off // 空行跳过
				}
			case '\r':
				m.crOff, m.mode = m.off-1, ModeCR
				if m.recStarted {
					m.emit(m.off - 1) // 逗号后的空字段；行首则等待判定是否空行
				}
			case '"':
				m.mode, m.active, m.quoted, m.recStarted = ModeQuoted, true, true, true
			default:
				m.mode, m.active, m.recStarted = ModeUnquoted, true, true
				if e := m.add(b); e != nil { return e }
			}
		case ModeUnquoted:
			switch b {
			case ',':
				if e := m.comma(); e != nil { return e }
			case '\n':
			m.emit(m.off - 1)
				if e := m.rowEnd(m.off-1); e != nil { return e }
			case '\r':
				m.crOff, m.mode = m.off-1, ModeCR
				m.emit(m.off - 1)
			case '"':
				return m.fail(ErrBareQuote)
			default:
				if e := m.add(b); e != nil { return e }
			}
		case ModeQuoted:
			if b == '"' {
				m.mode = ModeQuoteSeen
			} else if e := m.add(b); e != nil {
				return e
			}
		case ModeQuoteSeen:
			switch b {
			case ',':
				if e := m.comma(); e != nil { return e }
			case '\n':
			m.emit(m.off - 1)
				if e := m.rowEnd(m.off-1); e != nil { return e }
			case '\r':
				m.crOff, m.mode = m.off-1, ModeCR
				m.emit(m.off - 1)
			case '"':
				m.mode = ModeQuoted
				if e := m.add('"'); e != nil { return e }
			default:
				return m.fail(ErrQuoteAfterField)
			}
		case ModeCR:
			if b != '\n' {
				m.off = m.crOff
				return m.fail(ErrBadCR)
			}
			if m.recStarted {
			if e := m.rowEnd(m.off - 1); e != nil { return e }
			} else {
				m.fstart, m.mode = m.off, ModeFieldStart // 空行跳过
			}
		}
	}
	return nil
}

func (m *machine) finish() *Error {
	if m.terminal != nil {
		return m.terminal
	}
	switch m.mode {
	case ModeQuoted:
		return m.fail(ErrUnterminated)
	case ModeCR:
		m.off = m.crOff
		return m.fail(ErrBadCR)
	case ModeFieldStart:
		if m.recStarted {
			m.emit(m.off)
			return m.rowEnd(m.off)
		}
	case ModeUnquoted, ModeQuoteSeen:
		m.emit(m.off)
		return m.rowEnd(m.off)
	}
	return nil
}

// RunSegment 在给定起始假设下解析 data；final 为真时按流结束处理。
func RunSegment(data []byte, seed Seed, final bool, cfg Config) *Result {
	cap := &capture{}
	m := newMachine(cfg, cap, seed)
	err := m.feed(data)
	if err == nil && final {
		err = m.finish()
	}
		r := &Result{Events: cap.events, Err: err, Mode: m.mode,
		RecNo: m.recNo, FldNo: m.fldNo, FLen: m.flen, Active: m.active,
		RecStarted: m.recStarted, RowsDone: m.rowsDone, FieldsDone: m.fieldsDone,
		OpenBuf: append([]byte(nil), m.buf...),
		OpenStart: m.fstart, Ops: m.ops}
	return r
}

type capture struct{ events []Ev }

func (c *capture) Emit(cell cell.Cell) {
	c.events = append(c.events, Ev{Cell: cell})
}

func (c *capture) EndRecordAt(off int) {
	c.events = append(c.events, Ev{End: true, EndOff: off})
}
	return nil
}

// Machine 是单线程流式解析器；单实例不要求并发安全。
type Machine struct {
	m *machine
}

func NewMachine(cfg Config, sink Sink) *Machine {
	return &Machine{newMachine(cfg, sink, Seed{RecNo: 1, FldNo: 1})}
}

func (M *Machine) Feed(p []byte) error { return M.m.feed(p) }
func (M *Machine) Close() error        { return M.m.finish() }

// Ops 返回字节被状态机处理的总次数。
func (M *Machine) Ops() int64 { return M.m.ops }
